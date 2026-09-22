package config

import (
	"fmt"
	"sort"
	"sync"
)

// StoreKind is how a platform holds a configuration store, which decides which
// of that platform's services a value is read from.
//
// Opaque here. The names are a provider's own, "secrets-manager" and
// "parameter-store" being two of AWS's, this is a value the deployment
// set through to the provider, which is the only thing that can interpret it.
// Empty means the provider's own default.
type StoreKind string

// Provider builds the config backends for one platform.
//
// Implemented by a provider module and registered from its init, so that
// selecting one is an import rather than something an application wires up,
// exactly as a serverless adapter and a resource provider are.
// Core doesn't import any provider-specific SDKs.
type Provider interface {
	// Name identifies the provider in errors, such as "aws".
	Name() string
	// Platform is the platform this provider serves, matched against what the
	// deployment says the application is running on.
	Platform() Platform
	// Backend returns the backend reading a store of this kind.
	//
	// A kind the platform does not hold configuration in is an error rather
	// than a silently empty store, since it means the deployment asked for
	// something this provider cannot do.
	Backend(kind StoreKind) (Backend, error)
}

var (
	providerMu sync.RWMutex
	providers  []Provider
)

// RegisterProvider adds a provider to the set considered at startup.
//
// Called from a provider package's init, and panics on a duplicate platform,
// which can only mean two providers claiming one.
func RegisterProvider(p Provider) {
	providerMu.Lock()
	defer providerMu.Unlock()

	for _, existing := range providers {
		if existing.Platform() == p.Platform() {
			panic(fmt.Sprintf(
				"celerity: config providers %q and %q both serve platform %q",
				existing.Name(), p.Name(), p.Platform(),
			))
		}
	}
	providers = append(providers, p)
}

// RegisteredProviders returns the platforms configuration can be read from in
// this binary, sorted.
func RegisteredProviders() []string {
	providerMu.RLock()
	defer providerMu.RUnlock()

	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Name())
	}
	sort.Strings(names)
	return names
}

// providerFor returns the provider serving a platform.
func providerFor(platform Platform) (Provider, bool) {
	providerMu.RLock()
	defer providerMu.RUnlock()

	for _, p := range providers {
		if p.Platform() == platform {
			return p, true
		}
	}
	return nil, false
}

// SelfCaching is implemented by a backend that already holds values between
// reads.
//
// Refreshing on top of one costs a request to re-read a cache rather than the
// store, so a backend that does its own caching says so and this package leaves
// it alone. Whether that applies is the provider's to know: on AWS it depends on
// whether a serverless function runs behind the parameters and secrets extension, which
// is a thing core should not be able to tell.
type SelfCaching interface {
	Backend
	// Caching reports that values are already held between reads.
	Caching() bool
}

// backendFor returns the backend a store should be read through.
func backendFor(platform Platform, kind StoreKind) (Backend, error) {
	p, ok := providerFor(platform)
	if !ok {
		return EmptyBackend{}, nil
	}

	backend, err := p.Backend(kind)
	if err != nil {
		return nil, fmt.Errorf("celerity: %s cannot read a %q store: %w", p.Name(), kind, err)
	}
	return backend, nil
}

// caches reports whether a backend holds values between reads itself.
func caches(backend Backend) bool {
	c, ok := backend.(SelfCaching)
	return ok && c.Caching()
}
