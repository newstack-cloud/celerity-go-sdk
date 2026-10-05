// Package cache is how a Celerity application reaches an ElastiCache cluster.
//
// There is very little here, and that is the point. ElastiCache speaks the
// Redis protocol, as does every other platform's managed cache, so the cache
// itself lives in resources/redis and is the same code on every deploy target.
// What is AWS's is how a password is obtained: read from Secrets Manager, or
// signed from the execution role's own identity.
package cache

import (
	"context"
	"errors"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/redis"
)

// Linking this package is what lets an application reach a cache on AWS.
// `celerity-go generate` writes the import when the blueprint declares one.
func init() {
	service.RegisterCache(func(s *service.Session) service.Builder[cache.Client] {
		return func(ref resources.Ref) (cache.Client, error) {
			client, err := redis.New(ref, credentials{session: s, ref: ref})
			if err != nil {
				return nil, err
			}
			track(client.(redis.Closeable))
			return client, nil
		}
	})
	service.RegisterReleaser(release)
}

// The caches built, so that the connections they opened can be given back when
// the application is shut down. A handle is taken during registration and held
// for the life of the process, so nothing is ever removed from this.
var (
	builtMu sync.Mutex
	built   []redis.Closeable
)

func track(client redis.Closeable) {
	builtMu.Lock()
	defer builtMu.Unlock()
	built = append(built, client)
}

// release closes every connection a cache opened. One that was never reached
// opened none and closing it does nothing.
func release(context.Context) error {
	builtMu.Lock()
	clients := make([]redis.Closeable, len(built))
	copy(clients, built)
	builtMu.Unlock()

	var errs []error
	for _, client := range clients {
		errs = append(errs, client.Close())
	}
	return errors.Join(errs...)
}

// credentials is what AWS contributes to a cache: the two ways a password is
// obtained here, and nothing about Redis.
type credentials struct {
	session *service.Session
	ref     resources.Ref
}

// Secret reads a password the deployment left a Secrets Manager id for.
func (c credentials) Secret(ctx context.Context, id string) (string, error) {
	return c.session.SecretString(ctx, id)
}

// SignedPassword mints a connection token from the execution role's identity,
// which is how a cache is reached with IAM authentication rather than with a
// password anything could have copied.
func (c credentials) SignedPassword(
	host, user, region string,
) func(context.Context) (string, string, error) {
	tokens := &elastiCacheTokens{
		session: c.session,
		host:    host,
		user:    user,
		region:  region,
	}
	return tokens.credentials
}
