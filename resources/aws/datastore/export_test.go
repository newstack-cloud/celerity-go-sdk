package datastore

import (
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// Stores returns a builder driving the given API, so that a suite exercises the
// real store against a stand-in rather than against DynamoDB.
func Stores(api API) func(resources.Ref) (datastore.Client, error) {
	return (&stores{api: api}).build
}
