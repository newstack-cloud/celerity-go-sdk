package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// A resource package registers what it can build, and the provider asks here
// rather than importing the package itself.
//
// That indirection is the whole point of the split: a build links the AWS
// service clients for the resources the blueprint declares and no others, so an
// application with a data store and nothing else does not carry S3, SQS, SNS and
// a Redis client it never calls. `celerity-go generate` writes an import per
// resource kind for that reason.
//
// One slot per kind rather than a map of any, so that the provider's methods
// stay typed and a registration that does not match is a compile error.
type Builder[T any] func(resources.Ref) (T, error)

// Factory makes a builder for one provider's session. A resource package
// registers this, and the provider calls it once, so a lazily built service
// client is shared by every handle of that kind.
type Factory[T any] func(*Session) Builder[T]

var (
	buckets    Factory[bucket.Store]
	queues     Factory[queue.Client]
	topics     Factory[topic.Client]
	caches     Factory[cache.Client]
	datastores Factory[datastore.Client]
	databases  Factory[sqldb.Client]
)

// RegisterBucket is called by resources/aws/bucket when it is linked.
func RegisterBucket(f Factory[bucket.Store]) { buckets = f }

// RegisterQueue is called by resources/aws/queue when it is linked.
func RegisterQueue(f Factory[queue.Client]) { queues = f }

// RegisterTopic is called by resources/aws/topic when it is linked.
func RegisterTopic(f Factory[topic.Client]) { topics = f }

// RegisterCache is called by resources/aws/cache when it is linked.
func RegisterCache(f Factory[cache.Client]) { caches = f }

// RegisterDatastore is called by resources/aws/datastore when it is linked.
func RegisterDatastore(f Factory[datastore.Client]) { datastores = f }

// RegisterSQLDatabase is called by resources/aws/sqldb when it is linked.
func RegisterSQLDatabase(f Factory[sqldb.Client]) { databases = f }

// Buckets returns the builder the bucket package registered.
func Buckets(s *Session) (Builder[bucket.Store], error) {
	return builderFor(buckets, s, resources.KindBucket, "bucket")
}

// Queues returns the builder the queue package registered.
func Queues(s *Session) (Builder[queue.Client], error) {
	return builderFor(queues, s, resources.KindQueue, "queue")
}

// Topics returns the builder the topic package registered.
func Topics(s *Session) (Builder[topic.Client], error) {
	return builderFor(topics, s, resources.KindTopic, "topic")
}

// Caches returns the builder the cache package registered.
func Caches(s *Session) (Builder[cache.Client], error) {
	return builderFor(caches, s, resources.KindCache, "cache")
}

// Datastores returns the builder the datastore package registered.
func Datastores(s *Session) (Builder[datastore.Client], error) {
	return builderFor(datastores, s, resources.KindDatastore, "datastore")
}

// SQLDatabases returns the builder the sqldb package registered.
func SQLDatabases(s *Session) (Builder[sqldb.Client], error) {
	return builderFor(databases, s, resources.KindSQLDatabase, "sqldb")
}

// builderFor reports an unlinked resource package as what it is: a build that
// did not import what the blueprint asked for. Naming the import is the whole
// of the fix, and the alternative is a nil client failing later with nothing to
// say about why.
func builderFor[T any](
	f Factory[T], s *Session, kind resources.Kind, pkg string,
) (Builder[T], error) {
	if f == nil {
		return nil, fmt.Errorf(
			"celerity: this build reaches a %s resource and does not link the package that "+
				"serves one: import _ \"github.com/newstack-cloud/celerity-go-sdk/resources/aws/%s\", "+
				"which celerity-go generate writes from the blueprint",
			kind, pkg)
	}
	return f(s), nil
}

// Releasers are what the linked resource packages hold open, which the provider
// gives back when an application is shut down.
//
// A slice rather than a slot per kind: closing is the same thing whatever the
// resource is, and a package registers one of these only if it holds something,
// which most do not. Registered from an init, so no lock is needed: the
// registrations are done before anything serves.
var releasers []func(context.Context) error

// RegisterReleaser is called by a resource package that holds a connection pool
// or a client worth giving back on shutdown.
func RegisterReleaser(release func(context.Context) error) {
	releasers = append(releasers, release)
}

// Release gives back everything the linked resource packages hold.
//
// Every releaser is called even when an earlier one fails: shutdown is the one
// time carrying on matters more than the error.
func Release(ctx context.Context) error {
	var errs []error
	for _, release := range releasers {
		if err := release(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
