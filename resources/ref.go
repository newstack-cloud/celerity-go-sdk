package resources

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// Ref is the blueprint resource a handle was taken for, and how a provider
// finds out what the deployment actually called it.
//
// A provider is given one of these rather than a name, and nothing is resolved
// when it is built. The provider holds it and asks it on first use, so a handle
// taken during registration does not need to make a request to a config store.
type Ref struct {
	// Kind is the resource type, and is checked against the topology rather than
	// trusted: a name belonging to a queue and asked for as a bucket is a
	// mistake in the handler.
	Kind Kind
	// Name is the name the blueprint gave the resource, which is the only name
	// the application knows. What the resource is called once it exists is
	// decided by the deployment.
	Name string
	// Config is what the deployment recorded. This includes which key
	// holds the resource, and the identifiers themselves.
	// Nil only where an application was built
	// without configuration, which is reported as the resource being
	// unresolvable rather than dereferenced.
	Config *config.Service
}

// ID returns the identifier the deployment gave the resource, for example,
// a bucket's name, a queue's URL, a topic's ARN, a table's name.
func (r Ref) ID(ctx context.Context) (string, error) {
	if r.Config == nil {
		return "", r.unresolvable()
	}

	return r.Config.Resource(ctx, string(r.Kind), r.Name)
}

// Field returns one of several values recorded about a resource, by the suffix
// it was written under.
//
// Most resources are a single identifier, which is [Ref.ID]. A cache or a
// database is an endpoint, a port, a user and how to authenticate, and the
// deploy engine writes each under the resource's own key:
//
//	ref.Field(ctx, "_host")
//
// Reports absent rather than failing where the deployment recorded nothing,
// since most of these have a default and only the provider knows which of them
// the resource cannot do without.
func (r Ref) Field(ctx context.Context, suffix string) (string, bool, error) {
	if r.Config == nil {
		return "", false, r.unresolvable()
	}

	key, err := r.Config.ResourceKey(string(r.Kind), r.Name)
	if err != nil {
		return "", false, err
	}

	ns, err := r.Config.Namespace(config.ResourcesNamespace)
	if err != nil {
		return "", false, err
	}

	return ns.Lookup(ctx, key+suffix)
}

// String names the resource by its blueprint name, which is the one a handler
// asked for and the one an error should point at.
func (r Ref) String() string {
	return fmt.Sprintf("%s %q", r.Kind, r.Name)
}

func (r Ref) unresolvable() error {
	return fmt.Errorf(
		"celerity: %s cannot be resolved because the application has no configuration, "+
			"so nothing holds what the deployment called it",
		r,
	)
}
