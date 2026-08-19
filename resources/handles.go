package resources

import "fmt"

// Provider builds resource clients for one platform.
//
// It is what a provider module such as resources/aws implements, and it is
// supplied to the application at startup, so core never imports a cloud SDK.
type Provider interface {
	Name() string
	Bucket(name string) (BucketStore, error)
	Queue(name string) (QueueClient, error)
	Topic(name string) (TopicClient, error)
	Cache(name string) (CacheClient, error)
	Datastore(name string) (DatastoreClient, error)
	SQLDatabase(name string) (SQLDatabaseClient, error)
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
func Bucket(host Host, name ...string) BucketStore {
	return resolve(host, KindBucket, name, func(p Provider, n string) (BucketStore, error) {
		return p.Bucket(n)
	})
}

// Queue returns a handle to a blueprint queue resource.
func Queue(host Host, name ...string) QueueClient {
	return resolve(host, KindQueue, name, func(p Provider, n string) (QueueClient, error) {
		return p.Queue(n)
	})
}

// Topic returns a handle to a blueprint topic resource.
func Topic(host Host, name ...string) TopicClient {
	return resolve(host, KindTopic, name, func(p Provider, n string) (TopicClient, error) {
		return p.Topic(n)
	})
}

// Cache returns a handle to a blueprint cache resource.
func Cache(host Host, name ...string) CacheClient {
	return resolve(host, KindCache, name, func(p Provider, n string) (CacheClient, error) {
		return p.Cache(n)
	})
}

// Datastore returns a handle to a blueprint data store resource.
func Datastore(host Host, name ...string) DatastoreClient {
	return resolve(host, KindDatastore, name, func(p Provider, n string) (DatastoreClient, error) {
		return p.Datastore(n)
	})
}

// SQLDatabase returns a handle to a blueprint SQL database resource.
func SQLDatabase(host Host, name ...string) SQLDatabaseClient {
	return resolve(host, KindSQLDatabase, name, func(p Provider, n string) (SQLDatabaseClient, error) {
		return p.SQLDatabase(n)
	})
}

// resolve records the reference, then builds the client. The reference is
// recorded whether or not a provider can be found: extraction runs with none
// linked, and the reference is the point of that run.
func resolve[T any](host Host, kind Kind, name []string, build func(Provider, string) (T, error)) T {
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

	client, err := build(provider, resolved)
	if err != nil {
		host.ResourceError(fmt.Errorf("building %s %q: %w", kind, resolved, err))
		return zero
	}
	return client
}
