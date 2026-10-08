package redis

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// Credentials is how a cache's password is obtained on a platform where the
// deployment did not record a literal one.
type Credentials interface {
	// Secret reads a password the deployment recorded by reference rather than
	// by value, from wherever that platform keeps one.
	Secret(ctx context.Context, id string) (string, error)
	// SignedPassword mints a fresh password per connection, for a cache reached
	// with the platform's own identity rather than with a password. Nil says
	// the platform doesn't support obtaining signed passwords.
	//
	// This is per connection rather than once, because a signed token is shorter-lived
	// than the execution environment that holds the pool.
	SignedPassword(host, user, region string) func(context.Context) (string, string, error)
}

// Closeable is a cache client that also hands back the connection it holds,
// which is what [New] returns.
//
// Separate from [cache.Client] because closing a cache is not something a
// handler does: a handle is taken during registration and held for the life of
// the process, and what gives the sockets back is the application shutting
// down, which the provider module handles.
type Closeable interface {
	cache.Client
	Close() error
}

// Stated so that a provider module asserting to [Closeable] relies on something
// this package is held to.
var _ Closeable = (*redisCache)(nil)

// New returns a cache reached over Redis, built from what the deployment
// recorded about it.
//
// Credentials may be nil, which is a cache reached with a recorded password or
// with none. Nothing is read and no connection is made here, a handle is taken
// during registration, before an event has arrived.
func New(ref resources.Ref, creds Credentials) (cache.Client, error) {
	return &redisCache{ref: ref, creds: creds}, nil
}
