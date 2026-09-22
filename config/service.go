package config

import (
	"context"
	"sort"
	"sync"
)

// ResourcesNamespace is the namespace holding the identifiers of deployed
// infrastructure.
//
// Populated by the deployment rather than by the application: the blueprint
// names a bucket, and what the bucket is actually called is decided when it is
// created. A provider module reads it through [Service.Resource] rather than
// reading this namespace itself.
const ResourcesNamespace = "resources"

// Service is the configuration an application reads.
//
// Ordinarily it holds two stores: [ResourcesNamespace], which the deploy engine
// describes and which holds the identifiers of deployed infrastructure, and one
// the blueprint declared for the application. The application's own is read
// straight off the service, since the resources store is not one it declared;
// an application with several of its own names the one it means.
type Service struct {
	mu         sync.RWMutex
	namespaces map[string]Namespace
	links      Links
}

// New returns a service holding no namespaces.
//
// An application does not normally build one: celerity.New does, from what the
// deployment gave it. A test builds one with [Service.Register] and a
// [MapBackend].
func New() *Service {
	return &Service{namespaces: make(map[string]Namespace)}
}

// Register adds a namespace under a name, replacing one already there.
func (s *Service) Register(name string, ns Namespace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.namespaces[name] = ns
}

// WithLinks records the resource topology the Celerity CLI wrote into the
// bundle, which is what [Service.Resource] resolves against.
func (s *Service) WithLinks(links Links) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links = links
	return s
}

// Namespace returns a namespace by name.
func (s *Service) Namespace(name string) (Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if ns, ok := s.namespaces[name]; ok {
		return ns, nil
	}
	return nil, &MissingNamespaceError{Name: name, Registered: s.registered()}
}

// Registered returns the namespaces held, sorted.
func (s *Service) Registered() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.registered()
}

func (s *Service) registered() []string {
	names := make([]string, 0, len(s.namespaces))
	for name := range s.namespaces {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns a value from the one namespace registered.
func (s *Service) Lookup(ctx context.Context, key string) (string, bool, error) {
	ns, err := s.only()
	if err != nil {
		return "", false, err
	}
	return ns.Lookup(ctx, key)
}

// Get returns a value from the one namespace registered.
func (s *Service) Get(ctx context.Context, key string) (string, error) {
	ns, err := s.only()
	if err != nil {
		return "", err
	}
	return ns.Get(ctx, key)
}

// All returns every value in the one namespace registered.
func (s *Service) All(ctx context.Context) (map[string]string, error) {
	ns, err := s.only()
	if err != nil {
		return nil, err
	}
	return ns.All(ctx)
}

// Bind fills a struct from the one namespace registered.
func (s *Service) Bind(ctx context.Context, out any) error {
	ns, err := s.only()
	if err != nil {
		return err
	}
	return ns.Bind(ctx, out)
}

// only returns the application's single namespace, and refuses to guess between
// several.
//
// Reading from whichever happened to be registered first would be a value from
// the wrong store, which is worse than being told to name one: the application
// would run, and read something plausible.
//
// The resources namespace does not count towards the choice, which is what
// makes the ordinary two-store shape read as one: it is described by the deploy
// engine rather than declared by the blueprint, so an application with a single
// celerity/config resource still has exactly one store of its own.
func (s *Service) only() (Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var found Namespace
	var names []string
	for name, ns := range s.namespaces {
		if name == ResourcesNamespace {
			continue
		}
		found = ns
		names = append(names, name)
	}

	switch len(names) {
	case 0:
		return nil, &MissingNamespaceError{Registered: s.registered()}
	case 1:
		return found, nil
	default:
		sort.Strings(names)
		return nil, &AmbiguousNamespaceError{Registered: names}
	}
}

// Resource returns the identifier of a deployed resource, by the name the
// blueprint gave it.
//
// Two steps, and both are needed. The links file says which config key holds
// this resource's identifier, and the resources namespace holds the identifier
// itself: the first is written when the application is built, the second when
// the infrastructure is deployed, and only the pair of them can say that
// ordersBucket is a bucket in S3 called orders-bucket-prod-eu-west-2.
//
// This is what a provider module calls, and why resources/aws doesn't need to
// know about how a deployment names things.
func (s *Service) Resource(ctx context.Context, kind, name string) (string, error) {
	s.mu.RLock()
	links := s.links
	s.mu.RUnlock()

	link, err := links.Find(kind, name)
	if err != nil {
		return "", err
	}

	ns, err := s.Namespace(ResourcesNamespace)
	if err != nil {
		return "", err
	}
	return ns.Get(ctx, link.ConfigKey)
}
