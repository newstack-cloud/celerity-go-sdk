package resources

import (
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Provider builds resource clients for one platform.
//
// It is what a provider module such as resources/aws implements, and it is
// supplied to the application at startup, so core never imports a cloud SDK.
type Provider interface {
	Name() string
	Bucket(Ref) (bucket.Store, error)
	Queue(Ref) (queue.Client, error)
	Topic(Ref) (topic.Client, error)
	Cache(Ref) (cache.Client, error)
	Datastore(Ref) (datastore.Client, error)
	SQLDatabase(Ref) (sqldb.Client, error)
}

// Host is the part of an application a resource handle needs.
//
// It is an interface so that this package does not depend on the celerity
// package; *celerity.App satisfies it.
type Host interface {
	// ResourceProvider returns the provider an application was given
	// explicitly, and nil when it was given none, which is the ordinary case:
	// the provider is normally the linked one.
	ResourceProvider() Provider
	// Config is what the deployment recorded about the application, which is
	// where a blueprint name is resolved to the identifier the resource was
	// created under. Passed on to the provider through a [Ref] rather than read
	// here, nothing is resolved while handles are being taken.
	Config() *config.Service
	// RecordResourceRef notes that the application reaches a resource, which is
	// reported by the manifest and cross-checked against the static extraction
	// pass.
	RecordResourceRef(kind Kind, name string)
	// ResourceError reports a resource that could not be built. Handles are
	// taken during registration, where returning an error to every call site
	// would drown the registration list, so errors are collected and reported
	// together by celerity.Run.
	ResourceError(err error)
	// Extracting reports that the process is describing itself for the CLI
	// rather than serving. A missing provider is expected then: extraction wants
	// the references, and building clients would need credentials on a machine
	// that has no reason to hold any.
	Extracting() bool
}

// providerFor prefers a provider an application was given explicitly, which is
// what a test supplying a fake wants, and falls back to the linked one.
func providerFor(host Host) Provider {
	if p := host.ResourceProvider(); p != nil {
		return p
	}
	if p, ok := DetectedProvider(); ok {
		return p
	}
	return nil
}

// Bucket returns a handle to a blueprint bucket resource.
//
// Called with no name it refers to the only bucket the blueprint declares,
// which the CLI resolves.
func Bucket(host Host, name ...string) bucket.Store {
	return resolve(host, KindBucket, name, func(p Provider, r Ref) (bucket.Store, error) {
		return p.Bucket(r)
	})
}

// Queue returns a handle to a blueprint queue resource.
func Queue(host Host, name ...string) queue.Client {
	return resolve(host, KindQueue, name, func(p Provider, r Ref) (queue.Client, error) {
		return p.Queue(r)
	})
}

// Topic returns a handle to a blueprint topic resource.
func Topic(host Host, name ...string) topic.Client {
	return resolve(host, KindTopic, name, func(p Provider, r Ref) (topic.Client, error) {
		return p.Topic(r)
	})
}

// Cache returns a handle to a blueprint cache resource.
func Cache(host Host, name ...string) cache.Client {
	return resolve(host, KindCache, name, func(p Provider, r Ref) (cache.Client, error) {
		return p.Cache(r)
	})
}

// Datastore returns a handle to a blueprint data store resource.
func Datastore(host Host, name ...string) datastore.Client {
	return resolve(host, KindDatastore, name, func(p Provider, r Ref) (datastore.Client, error) {
		return p.Datastore(r)
	})
}

// SQLDatabase returns a handle to a blueprint SQL database resource.
func SQLDatabase(host Host, name ...string) sqldb.Client {
	return resolve(host, KindSQLDatabase, name, func(p Provider, r Ref) (sqldb.Client, error) {
		return p.SQLDatabase(r)
	})
}

// resolve records the reference, then builds the client. The reference is
// recorded whether or not a provider can be found as extraction runs with none
// linked, and the reference is the point of that run.
func resolve[T any](host Host, kind Kind, name []string, build func(Provider, Ref) (T, error)) T {
	var zero T
	resolved := DefaultName
	if len(name) > 0 && name[0] != "" {
		resolved = name[0]
	}
	host.RecordResourceRef(kind, resolved)

	provider := providerFor(host)
	if provider == nil {
		if !host.Extracting() {
			host.ResourceError(&MissingProviderError{
				Kind:   kind,
				Name:   resolved,
				Linked: RegisteredProviders(),
			})
		}
		return zero
	}

	ref := Ref{Kind: kind, Name: resolved, Config: host.Config()}
	client, err := build(provider, ref)
	if err != nil {
		host.ResourceError(fmt.Errorf("building %s: %w", ref, err))
		return zero
	}
	return client
}
