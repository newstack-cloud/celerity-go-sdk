// Package config gives a handler its configuration including the values a
// celerity/config resource holds, and the identifiers of the infrastructure the
// application reaches.
//
// A handler asks for a value and does not need to know where it came from. Under the
// Celerity runtime that is whatever store the deployment configured; under a
// serverless platform it is the provider's own parameter or secret store; in a
// test it is a map. What differs is the [Backend], which is supplied at startup
// so that core imports no provider's SDK.
//
// # How the stores are wired up
//
// An application doesn't configure anything. A blueprint declares celerity/config resources,
// the CLI provisions the store and wires up the application at build time to access config.
//
// # How a handler reaches it
//
// By being given it. A [Service] exists before any event does and is the same
// object for every one of them, so it is a dependency rather than anything
// about a request, and it arrives the way every other dependency in this SDK
// arrives:
//
//	celerity.Get(app, "/orders", orders.List(store, cfg))
//
// # Resolving a resource
//
// The identifiers of deployed infrastructure are configuration like any other.
// A blueprint names a bucket ordersBucket; the bucket that exists is called
// something the deployment chose, and the two are joined by the links file the
// Celerity CLI writes into the bundle at build time. A provider module such as
// resources/aws reads the link and then reads the identifier out of the
// resources namespace, which is why it needs no knowledge of how a deployment
// names things.
package config

import (
	"context"
	"maps"
)

// Backend fetches the values one store holds.
//
// This is implemented per platform, it could be a parameter store, a secret manager, a file, a map.
// One call returns everything in the store rather than one key, because a lot
// of backends charge per request rather than per value, and a handler
// reading three keys should not pay for three round trips.
type Backend interface {
	// Fetch returns every value in the store.
	//
	// A store that does not exist is an error. A store that exists and is empty
	// is an empty map, which is not the same thing, as the first is a deployment
	// that did not happen, and reporting it as the second would leave a handler
	// reading absent values it was promised.
	Fetch(ctx context.Context, storeID string) (map[string]string, error)
}

// MapBackend serves values held in memory, which is what a test wants and what
// a local run can use before anything is deployed.
type MapBackend map[string]map[string]string

// Fetch returns the values held for a store.
func (b MapBackend) Fetch(_ context.Context, storeID string) (map[string]string, error) {
	values, ok := b[storeID]
	if !ok {
		return nil, &MissingStoreError{StoreID: storeID}
	}

	// Copied, so that a caller holding the result cannot change what the next
	// read sees.
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out, nil
}

// EmptyBackend holds nothing and reports every store as empty rather than
// absent.
//
// It is what an application with no config resource runs against, so that
// asking for a value it never declared reads as the value being absent rather
// than as the deployment being broken.
type EmptyBackend struct{}

// Fetch returns no values.
func (EmptyBackend) Fetch(context.Context, string) (map[string]string, error) {
	return map[string]string{}, nil
}
