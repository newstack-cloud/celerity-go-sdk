package service

import (
	"context"
	"sync"
)

// ClientKey is what a cached service client is built for.
//
// A struct of one field rather than a region string, because what distinguishes
// two clients is already expected to grow: reaching a resource in another
// account by assuming a role there would key on the identity the client signs
// with as well, and that is a field here rather than a different cache.
type ClientKey struct {
	// Region the client sends to. Empty is the application's own, which is what
	// a deployment records for everything it created for this application.
	Region string
}

// Clients caches one service client per key, building each on first use.
//
// A single client for the whole provider was enough while every resource was
// one a deployment created for this application, since those are all in the
// application's own region. A resource the blueprint declares as external may
// be elsewhere, and a service routes a request by the region the client was
// built for, so a client per region is what lets one application reach both.
//
// Clients are safe to share and expensive enough to be worth building once, so
// the entry for a key, including a failure to build it, is what every later
// caller for that key gets.
type Clients[T any] struct {
	mu    sync.Mutex
	built map[ClientKey]*entry[T]
}

type entry[T any] struct {
	once sync.Once
	val  T
	err  error
}

// Get returns the client for a key, building it on the first call for that key.
//
// The build runs outside the lock, so a client that is slow to construct does
// not hold up a caller wanting a different region.
func (c *Clients[T]) Get(_ context.Context, key ClientKey, build func() (T, error)) (T, error) {
	c.mu.Lock()
	if c.built == nil {
		c.built = make(map[ClientKey]*entry[T])
	}
	e, ok := c.built[key]
	if !ok {
		e = &entry[T]{}
		c.built[key] = e
	}
	c.mu.Unlock()

	e.once.Do(func() { e.val, e.err = build() })
	return e.val, e.err
}
