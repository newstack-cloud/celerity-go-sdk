package celeritytest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Provider hands an application test doubles in place of deployed resources.
//
// It is given to the application rather than registered, since a test says what
// its handlers reach rather than inferring it from an environment:
//
//	res := celeritytest.Resources()
//	app := orders.App(celerity.WithResourceProvider(res))
//
// A resource is made the first time a handle is taken for it and kept under
// that name, so the handle a handler closed over and the one a test reads
// afterwards are the same object.
type Provider struct {
	mu         sync.Mutex
	buckets    map[string]*Bucket
	queues     map[string]*Queue
	topics     map[string]*Topic
	datastores map[string]*Datastore
	caches     map[string]*CacheStub
	databases  map[string]sqldb.Client

	// live is what a kind with no double is handed to, and nil where every
	// kind is a double. Set once by [LiveWith] before the provider is used.
	live resources.Provider
	// t is the test live resources belong to, for refusing a double asked for
	// too late. Nil under doubles, where the order does not matter.
	t testing.TB
	// taken records the resources the application resolved a handle for.
	taken map[string]bool
	// closeOnce guards the live provider's release, which both a test
	// exercising shutdown and the end of the test itself ask for.
	closeOnce sync.Once
}

// Resources returns a provider holding no resources yet.
func Resources() *Provider {
	return &Provider{
		buckets:    map[string]*Bucket{},
		queues:     map[string]*Queue{},
		topics:     map[string]*Topic{},
		datastores: map[string]*Datastore{},
		caches:     map[string]*CacheStub{},
		databases:  map[string]sqldb.Client{},
		taken:      map[string]bool{},
	}
}

// Name identifies this provider in errors and in the handler manifest.
func (p *Provider) Name() string {
	return "celeritytest"
}

// Bucket returns the bucket an application took a handle for, making it on
// first use.
func (p *Provider) Bucket(ref resources.Ref) (bucket.Store, error) {
	p.took(resources.KindBucket, ref.Name)

	if double, ok := held(p, p.buckets, ref.Name); ok {
		return double, nil
	}
	if p.live != nil {
		return p.live.Bucket(ref)
	}
	return made(p, p.buckets, ref.Name, NewBucket), nil
}

// Queue returns the queue an application took a handle for.
func (p *Provider) Queue(ref resources.Ref) (queue.Client, error) {
	p.took(resources.KindQueue, ref.Name)

	if double, ok := held(p, p.queues, ref.Name); ok {
		return double, nil
	}
	if p.live != nil {
		return p.live.Queue(ref)
	}
	return made(p, p.queues, ref.Name, NewQueue), nil
}

// Topic returns the topic an application took a handle for.
func (p *Provider) Topic(ref resources.Ref) (topic.Client, error) {
	p.took(resources.KindTopic, ref.Name)

	if double, ok := held(p, p.topics, ref.Name); ok {
		return double, nil
	}
	if p.live != nil {
		return p.live.Topic(ref)
	}
	return made(p, p.topics, ref.Name, NewTopic), nil
}

// Datastore returns the data store an application took a handle for.
func (p *Provider) Datastore(ref resources.Ref) (datastore.Client, error) {
	p.took(resources.KindDatastore, ref.Name)

	if double, ok := held(p, p.datastores, ref.Name); ok {
		return double, nil
	}
	if p.live != nil {
		return p.live.Datastore(ref)
	}
	return made(p, p.datastores, ref.Name, NewDatastore), nil
}

// Cache returns the cache stub an application took a handle for.
func (p *Provider) Cache(ref resources.Ref) (cache.Client, error) {
	p.took(resources.KindCache, ref.Name)

	if double, ok := held(p, p.caches, ref.Name); ok {
		return double, nil
	}
	if p.live != nil {
		return p.live.Cache(ref)
	}
	return made(p, p.caches, ref.Name, NewCacheStub), nil
}

// SQLDatabase returns the database a test supplied with [Provider.WithDatabase],
// and otherwise one that refuses every call and says why.
func (p *Provider) SQLDatabase(ref resources.Ref) (sqldb.Client, error) {
	p.took(resources.KindSQLDatabase, ref.Name)

	if supplied, ok := held(p, p.databases, ref.Name); ok {
		return supplied, nil
	}
	if p.live != nil {
		return p.live.SQLDatabase(ref)
	}
	return refusingDatabase{name: ref.Name}, nil
}

// BucketNamed returns the bucket a blueprint calls name, making it where no
// handle has been taken for it yet so that a test can arrange its contents
// before the application reads them.
//
// Under live resources this is also how a double is substituted for one of
// them, and has to be called before the application takes its handles.
func (p *Provider) BucketNamed(name string) *Bucket {
	p.substituting(resources.KindBucket, name)
	return made(p, p.buckets, name, NewBucket)
}

// QueueNamed returns the queue a blueprint calls name.
func (p *Provider) QueueNamed(name string) *Queue {
	p.substituting(resources.KindQueue, name)
	return made(p, p.queues, name, NewQueue)
}

// TopicNamed returns the topic a blueprint calls name.
func (p *Provider) TopicNamed(name string) *Topic {
	p.substituting(resources.KindTopic, name)
	return made(p, p.topics, name, NewTopic)
}

// DatastoreNamed returns the data store a blueprint calls name.
func (p *Provider) DatastoreNamed(name string) *Datastore {
	p.substituting(resources.KindDatastore, name)
	return made(p, p.datastores, name, NewDatastore)
}

// CacheNamed returns the cache stub a blueprint calls name.
func (p *Provider) CacheNamed(name string) *CacheStub {
	p.substituting(resources.KindCache, name)
	return made(p, p.caches, name, NewCacheStub)
}

// WithDatabase hands the test's own database to the handlers that take a handle
// for the blueprint resource called name.
//
// There is no in-memory database to arrange, so this takes a real one: the
// engine a development session brings up, or whatever a suite starts for
// itself. A name no test supplied is refused rather than recorded, so a handler
// that reaches a database the suite did not arrange says so.
func (p *Provider) WithDatabase(name string, client sqldb.Client) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.databases[name] = client
	return p
}

// made returns the resource held under a name, building it on first use.
//
// Generic over the kind so that the near-identical bodies are one, and so that
// a kind added to the provider interface and forgotten here is a compile error.
func made[T any](p *Provider, held map[string]T, name string, build func(string) T) T {
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, ok := held[name]
	if !ok {
		existing = build(name)
		held[name] = existing
	}
	return existing
}

// Close gives back what the resources hold.
//
// Nothing, where they are doubles as they are maps in this process. Present even
// then so that a test exercising shutdown finds the same [resources.Closer] a
// real provider offers. Under live resources it releases the live provider's
// pools, once however many times it is called.
func (p *Provider) Close(ctx context.Context) error {
	var err error
	p.closeOnce.Do(func() {
		closer, holds := p.live.(resources.Closer)
		if p.live == nil || !holds {
			return
		}
		err = closer.Close(ctx)
	})
	return err
}

// What a stub answers for a call a test did not arrange.
//
// Named rather than nil, because a nil answer from a stub surfaces as a
// confusing zero value somewhere else, and the useful thing to say is which
// call the test did not account for.
func notConfigured(resource, call string) error {
	return fmt.Errorf(
		"celeritytest: %s was asked for %s, which this test did not arrange. "+
			"Set the matching function on the stub to say what it should answer",
		resource, call)
}
