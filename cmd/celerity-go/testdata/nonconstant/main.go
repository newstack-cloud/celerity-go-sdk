// Command nonconstant names a resource with something the type checker cannot
// tell the value of, which extraction refuses rather than dropping.
package main

import (
	"context"
	"os"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

func main() {
	app := celerity.New()

	// Read at runtime, so no analysis can say which resource this is.
	store := resources.Datastore(app, os.Getenv("TABLE"))

	celerity.Get(app, "/orders", func(ctx context.Context, _ struct{}) (int, error) {
		var out int
		_, err := store.Get(ctx, datastore.Key{Partition: "a"}, &out)
		return out, err
	}, celerity.Named("getOrders"))

	celerity.Run(app)
}
