package awstest

import (
	"context"
	"errors"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// Ref builds the pair a deployment writes, including the topology,
// saying which key holds a resource, and the values recorded under it.
func Ref(kind resources.Kind, name string, values map[string]string) resources.Ref {
	svc := config.New().WithLinks(config.Links{
		name: {Type: string(kind), ConfigKey: name},
	})
	svc.Register(
		config.ResourcesNamespace,
		config.NewNamespace(
			config.MapBackend{"resources": values},
			"resources",
		),
	)
	return resources.Ref{Kind: kind, Name: name, Config: svc}
}

// SimpleRef is the ordinary case: one identifier, under the resource's own key.
func SimpleRef(kind resources.Kind, name, id string) resources.Ref {
	return Ref(kind, name, map[string]string{name: id})
}

// Ctx is the background context, named for what every call needs one for.
func Ctx() context.Context {
	return context.Background()
}

// ErrNoCredentials stands in for anything that stops a credential being read.
var ErrNoCredentials = errors.New("no credentials")
