package redis_test

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

func ctx() context.Context {
	return context.Background()
}

// refFor builds the pair a deployment writes: the topology, saying which key
// holds a resource, and the values recorded under it.
func refFor(kind resources.Kind, name string, values map[string]string) resources.Ref {
	svc := config.New().WithLinks(config.Links{
		name: {Type: string(kind), ConfigKey: name},
	})
	svc.Register(config.ResourcesNamespace, config.NewNamespace(
		config.MapBackend{"resources": values}, "resources"),
	)

	return resources.Ref{Kind: kind, Name: name, Config: svc}
}

// recordedSecret is a password the deployment left a reference to, wherever the
// platform keeps one.
type recordedSecret struct {
	asked string
	value string
	err   error
}

// fakeCredentials is what a platform package supplies, with no platform behind
// it: this package's job is what it does with them, not where they come from.
type fakeCredentials struct {
	secret *recordedSecret
	signed func(context.Context) (string, string, error)
}

func (f *fakeCredentials) Secret(_ context.Context, id string) (string, error) {
	if f.secret == nil {
		return "", nil
	}
	f.secret.asked = id
	if f.secret.err != nil {
		return "", f.secret.err
	}
	return f.secret.value, nil
}

func (f *fakeCredentials) SignedPassword(
	_, _, _ string,
) func(context.Context) (string, string, error) {
	if f.signed != nil {
		return f.signed
	}
	return func(context.Context) (string, string, error) {
		return "orders-app", "signed", nil
	}
}
