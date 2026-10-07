package celeritytest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/config"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// Live returns resources backed by the services a `celerity dev test` session
// brought up, rather than by doubles.
//
// What serves them is the provider the test's build linked, found the way a
// deployed application finds it, so a handler under test resolves its resources
// through the same code a real session runs. Which means the test's build has to
// link them, as a session does:
//
//	import (
//		_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
//		_ "github.com/newstack-cloud/celerity-go-sdk/resources/local"
//		_ "github.com/newstack-cloud/celerity-go-sdk/resources/local/redis"
//	)
//
// A double can still be substituted per resource, by asking for one before the
// application takes its handles: [Provider.QueueNamed] and the rest make a
// double under that name and the live provider is not asked for it. So a suite
// can exercise a real database and keep the queue in memory.
//
// Releases what the provider holds when the test ends, since a live provider
// holds pools and sockets rather than maps.
func Live(t testing.TB) *Provider {
	t.Helper()

	provider, found := resources.DetectedProvider()
	if !found {
		t.Fatalf("celeritytest: %s", notDetected(resources.RegisteredProviders()))
	}
	return LiveWith(t, provider)
}

// LiveWith is [Live] against a provider named explicitly rather than the one the
// build linked.
//
// For a suite that builds its own provider, and for testing the harness itself.
func LiveWith(t testing.TB, provider resources.Provider) *Provider {
	t.Helper()

	p := Resources()
	p.live = provider
	p.t = t

	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Errorf("celeritytest: releasing the live provider: %v", err)
		}
	})
	return p
}

// Records that the application resolved a handle for a resource, so that a
// double asked for afterwards can be refused rather than quietly ignored.
func (p *Provider) took(kind resources.Kind, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.taken[string(kind)+"/"+name] = true
}

// substituting is called where a test asks for a double by name.
//
// Under live resources the order matters as a handle is taken during registration
// and held, so a double made after that is one nothing will ever reach. Under
// doubles it does not, because there is one object per name either way.
func (p *Provider) substituting(kind resources.Kind, name string) {
	if p.live == nil {
		return
	}

	p.mu.Lock()
	taken := p.taken[string(kind)+"/"+name]
	p.mu.Unlock()

	if !taken {
		return
	}
	p.t.Fatalf("celeritytest: the application already took a handle for %s %q, "+
		"and these are live resources, so it holds the live one. Ask for the double "+
		"before building the application, not after", kind, name)
}

// held reports the double a test put under a name, and whether there was one.
func held[T any](p *Provider, in map[string]T, name string) (T, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, ok := in[name]
	return existing, ok
}

// notDetected says why nothing serves live resources.
//
// Two different mistakes answer the same way from the registry, and they are
// fixed differently: a build that linked no provider has imports to add, and one
// that linked several has an environment to set, since which of them serves is
// the platform's answer rather than the import order's.
func notDetected(linked []string) string {
	if len(linked) == 0 {
		return "no resource provider is linked, so there is nothing to reach live " +
			"resources through. A test using Live links the providers a development " +
			"session links:\n\n" +
			"\timport (\n" +
			"\t\t_ \"github.com/newstack-cloud/celerity-go-sdk/resources/aws\"\n" +
			"\t\t_ \"github.com/newstack-cloud/celerity-go-sdk/resources/local\"\n" +
			"\t\t_ \"github.com/newstack-cloud/celerity-go-sdk/resources/local/redis\"\n" +
			"\t)\n\n" +
			"and runs with " + config.PlatformEnvVar + "=local."
	}

	return fmt.Sprintf(
		"resource providers are linked but none of them serves this platform, so "+
			"one was not selected: with more than one linked, which serves is read from "+
			"%s rather than from the import order. Set %s=local to reach what a "+
			"development session runs.\nLinked: %s",
		config.PlatformEnvVar, config.PlatformEnvVar, strings.Join(linked, ", "))
}
