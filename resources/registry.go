package resources

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Providers register themselves from an init function, so that an application
// selects one by importing it rather than by constructing it:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
//
// The same reasoning as the serverless adapters: what platform an application
// runs on is a deployment concern, and code that names it has to change when
// the deployment does.

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
