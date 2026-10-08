package redis

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// The whole of the interface is implemented here, across the files of this
// package.
var _ cache.Client = (*redisCache)(nil)

// A key-value cache reached over the Redis protocol.
//
// ElastiCache speaks the Redis protocol, and so does every other platform's
// managed cache and the Valkey instance a local session runs, which is why this is a
// Redis client rather than a service client.
//
// Unlike a bucket or a queue, a cache is not one identifier. The deployment
// records an endpoint, a port, whether the connection is encrypted, whether the
// cluster is sharded, and how to authenticate, each under the resource's own
// key.
type redisCache struct {
	creds Credentials
	ref   resources.Ref

	once      sync.Once
	client    goredis.UniversalClient
	keyPrefix string
	clustered bool
	connErr   error

	// opened is the client once it has been built, for a shutdown to read.
	//
	// The same client as the field above, held separately because that one is
	// written inside the Once and read by whoever ran it, whereas a shutdown
	// reads from another goroutine and must not consume the Once to find out
	// whether there is anything to close.
	opened atomic.Pointer[goredis.UniversalClient]
}

// Close gives back the sockets the connection holds.
//
// Exported on the implementation rather than put on [cache.Client], because
// closing a cache is not something a handler does: a handle is taken during
// registration and held for the life of the process, and what decides when it
// is given back is the application shutting down. A provider module calls this
// from its own shutdown.
//
// A cache that was never reached holds nothing and closing it does nothing, the
// client is built on the first call a handler makes.
func (c *redisCache) Close() error {
	client := c.opened.Load()
	if client == nil {
		return nil
	}

	if err := (*client).Close(); err != nil {
		return fmt.Errorf("celerity: closing the connection to %s: %w", c.ref, err)
	}
	return nil
}

// redisCommands is the commands this package sends, which both the plain client
// and the cluster client offer. Narrower than the client's own interface so
// that a helper taking one says what it needs.
type redisCommands = goredis.UniversalClient

// isNil reports the cache's own answer for something that holds nothing, which
// a few operations report as an empty result rather than as not-found.
func isNil(err error) bool {
	return errors.Is(err, goredis.Nil)
}

// connect builds the client once, on the first call a handler makes.
//
// The connection itself is not made here. A Redis client dials lazily and keeps
// a pool, so this resolves the configuration and hands back something that
// connects when it is first used, and reconnects for itself when needed.
func (c *redisCache) connect(ctx context.Context) (goredis.UniversalClient, error) {
	c.once.Do(func() {
		var options *goredis.UniversalOptions
		options, c.keyPrefix, c.connErr = c.options(ctx)
		if options != nil {
			c.clustered = options.IsClusterMode
		}
		if c.connErr != nil {
			return
		}

		c.client = goredis.NewUniversalClient(options)
		// Instrumented before anything is sent through it, so the first
		// command a handler makes is traced like every one after it.
		c.connErr = instrument(c.client)
		if c.connErr != nil {
			return
		}

		c.opened.Store(&c.client)
	})

	return c.client, c.connErr
}

func (c *redisCache) options(ctx context.Context) (*goredis.UniversalOptions, string, error) {
	settings, fields, err := c.recorded(ctx)
	if err != nil {
		return nil, "", err
	}

	options := &goredis.UniversalOptions{
		Addrs:    []string{net.JoinHostPort(settings.host, strconv.Itoa(settings.port))},
		Username: settings.user,
		// Which client this builds is decided here. A sharded cluster is
		// reached through its configuration endpoint, one address, so the
		// number of addresses cannot say: without this the client would be the
		// plain one, and a command for a key in another shard comes back as a
		// redirect that only the cluster client follows.
		IsClusterMode: settings.clustered,
	}
	if settings.encrypted() {
		options.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12, ServerName: settings.host,
		}
	}

	if err := c.authenticate(ctx, fields, settings, options); err != nil {
		return nil, "", err
	}

	return options, settings.keyPrefix, nil
}

func (c *redisCache) authenticate(
	ctx context.Context,
	f resources.Fields,
	settings recorded,
	options *goredis.UniversalOptions,
) error {
	if settings.authMode != resources.AuthIAM {
		token, err := c.password(ctx, f)
		if err != nil {
			return err
		}
		// An absent password is not an error, a cache with no auth configured,
		// which a local session's is, connects without one.
		options.Password = token
		return nil
	}

	signed, err := c.signer(ctx, f, settings.host, settings.user)
	if err != nil {
		return err
	}
	// A signed token is shorter-lived than the execution environment, so the
	// password cannot be a value as the pool outlives it and every new connection
	// has to sign a fresh one.
	options.CredentialsProviderContext = signed
	return nil
}

// signer is what mints a fresh password per connection, on a cache reached with
// the platform's own identity rather than with a recorded password.
func (c *redisCache) signer(
	ctx context.Context, f resources.Fields, host, user string,
) (func(context.Context) (string, string, error), error) {
	if user == "" {
		return nil, fmt.Errorf(
			"celerity: %s is reached with IAM authentication, which signs a token for a "+
				"particular user, and the deployment recorded no %q", c.ref, "_user")
	}

	region, err := f.Optional(ctx, "_region", "")
	if err != nil {
		return nil, err
	}

	if c.creds == nil {
		return nil, fmt.Errorf(
			"celerity: %s is reached with the platform's own identity, and this build links "+
				"no package that can sign for one: import the cache package for the platform "+
				"the blueprint deploys to", c.ref)
	}

	signed := c.creds.SignedPassword(host, user, region)
	if signed == nil {
		return nil, fmt.Errorf(
			"celerity: %s is reached with the platform's own identity, and the platform this "+
				"build is for has no such scheme", c.ref)
	}
	return signed, nil
}

// password is the one a deployment recorded, either written down or left as a
// reference the platform resolves.
func (c *redisCache) password(ctx context.Context, f resources.Fields) (string, error) {
	literal, err := f.Optional(ctx, "_authToken", "")
	if err != nil || literal != "" {
		return literal, err
	}

	id, err := f.Optional(ctx, "_authTokenSecretId", "")
	if err != nil || id == "" {
		return "", err
	}

	if c.creds == nil {
		return "", fmt.Errorf(
			"celerity: the password for %s is held in the platform's secret store, and this "+
				"build links no package that can read one", c.ref)
	}

	return c.creds.Secret(ctx, id)
}

// key is the name a command is actually sent under.
//
// A local development session gives each application its own prefix on one
// shared cache, so every key a handler names is relative to it. Applied here
// rather than by the client's own prefix option, because a key that comes back
// from the cache, as one does from a scan, has to have the prefix taken off
// again and the client would not do that.
func (c *redisCache) key(name string) string {
	return c.keyPrefix + name
}

// names applies the prefix to a list of keys, in order.
func (c *redisCache) names(keys []string) []string {
	prefixed := make([]string, len(keys))
	for i, key := range keys {
		prefixed[i] = c.key(key)
	}
	return prefixed
}

// strip takes the prefix off a key the cache returned, so that a handler only
// ever sees the names it used.
func (c *redisCache) strip(key string) string {
	return strings.TrimPrefix(key, c.keyPrefix)
}

// missing turns the cache's own answer for a key that holds nothing into the
// contract's, and leaves every other failure as the provider's own wrapped in
// the resource's name.
func (c *redisCache) missing(err error, doing, key string) error {
	if errors.Is(err, goredis.Nil) {
		return fmt.Errorf("celerity: %s holds nothing under %q: %w",
			c.ref, key, cache.ErrNotFound)
	}
	return c.failed(err, doing, key)
}

// failed wraps a provider error in what was being done and to what.
func (c *redisCache) failed(err error, doing, key string) error {
	if key == "" {
		return fmt.Errorf("celerity: %s in %s: %w", doing, c.ref, err)
	}

	return fmt.Errorf("celerity: %s %q in %s: %w", doing, key, c.ref, err)
}
