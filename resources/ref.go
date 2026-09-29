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
// when it is built. Handles are taken during registration, before an event has
// arrived and before anything has been read, and resolving here would put a
// request to a config store in the middle of a list of registrations. A
// provider holds the Ref and asks it on first use instead.
type Ref struct {
	// Kind is the resource type. It is checked against the topology rather than
	// trusted, a name belonging to a queue and asked for as a bucket is a
	// mistake in the handler, and resolving it would hand back a queue's URL to
	// something about to treat it as a bucket.
	Kind Kind
	// Name is the name the blueprint gave the resource, which is the only name
	// the application knows. What the resource is called once it exists is
	// decided by the deployment.
	Name string
	// Config is what the deployment recorded. This tells us which key
	// holds this resource, and the identifiers themselves. Nil only where an
	// application was built without configuration, which is reported as the
	// resource being unresolvable rather than dereferenced.
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
// Reports absent rather than failing where the deployment recorded nothing, since
// most of these have a default the provider applies, and only the provider
// knows which of them the resource cannot do without.
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

// String names the resource the way a handler asked for it, which is what an
// error should point at: the blueprint name is the only one the developer wrote.
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
