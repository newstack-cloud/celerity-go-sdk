package resources

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Providers register themselves from an init function, so that an application
// selects one by importing it rather than by constructing it:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/aws"

var (
	providerMu sync.RWMutex
	providers  []Provider
)

// RegisterProvider adds a resource provider to the set considered at startup.
//
// It is called from a provider package's init function and panics on a
// duplicate name.
func RegisterProvider(p Provider) {
	providerMu.Lock()
	defer providerMu.Unlock()

	for _, existing := range providers {
		if existing.Name() == p.Name() {
			panic(fmt.Sprintf("celerity: resource provider %q is registered twice", p.Name()))
		}
	}
	providers = append(providers, p)
}

// RegisteredProviders returns every linked provider, by name and sorted.
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

// DetectedProvider returns the provider for the platform this process runs on.
//
// With exactly one linked it is that one, whatever the environment says: a
// binary carrying only the AWS provider is being run against AWS, including
// from a developer's machine, where none of the platform's own variables are
// set.
func DetectedProvider() (Provider, bool) {
	providerMu.RLock()
	defer providerMu.RUnlock()

	if len(providers) == 1 {
		return providers[0], true
	}
	for _, p := range providers {
		if detector, ok := p.(interface{ Detect() bool }); ok && detector.Detect() {
			return p, true
		}
	}
	return nil, false
}

// FallbackProvider returns a linked provider other than the one named.
//
// What a provider serving only some of the resource kinds hands the rest to,
// for example, a development session serves a queue and a topic
// itself and delegates the other four to the platform's own provider.
//
// Reports false where nothing else is linked, which is a build that asked for a
// development session without a platform to fall back to.
func FallbackProvider(excluding string) (Provider, bool) {
	providerMu.RLock()
	defer providerMu.RUnlock()

	for _, p := range providers {
		if p.Name() != excluding {
			return p, true
		}
	}

	return nil, false
}

// MissingProviderError reports that a resource handle was taken but no provider
// can build it.
type MissingProviderError struct {
	Kind   Kind
	Name   string
	Linked []string
}

func (e *MissingProviderError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "no resource provider for %s %q.\n", e.Kind, e.Name)
	b.WriteString("Import the provider for the platform being deployed to, for example:\n\n")
	b.WriteString("\timport _ \"github.com/newstack-cloud/celerity-go-sdk/resources/aws\"\n")

	if len(e.Linked) > 1 {
		b.WriteString("\nMore than one provider is linked and none matched this environment: ")
		b.WriteString(strings.Join(e.Linked, ", "))
	}
	return b.String()
}

// Closer is a provider that holds something worth releasing when an application
// stops: a connection pool, a client with a background goroutine.
//
// Optional, and asserted for rather than part of [Provider]: a bucket and a
// queue are HTTP clients a process can simply stop using, where a database's
// open connections and a cache's sockets are worth giving back.
type Closer interface {
	Close(ctx context.Context) error
}

// Release gives back what the linked providers hold.
//
// Called by [celerity.Run] when serving ends, so an application does not have
// to. Providers that hold nothing are skipped, and a provider that fails to
// release is reported without stopping the rest.
//
// Nothing calls this on a serverless platform, where an execution environment
// is frozen between invocations and torn down without warning rather than
// shut down. A pool there is given up with the environment.
func Release(ctx context.Context) error {
	providerMu.RLock()
	holders := make([]Closer, 0, len(providers))
	for _, p := range providers {
		if closer, ok := p.(Closer); ok {
			holders = append(holders, closer)
		}
	}
	providerMu.RUnlock()

	var errs []error
	for _, holder := range holders {
		if err := holder.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
