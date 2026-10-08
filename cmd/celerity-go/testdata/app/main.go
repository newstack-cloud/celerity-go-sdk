// Provides an application extraction is run against.
//
// Every shape a handler can reach a resource through is here, since what the
// static pass has to get right is finding the handle rather than the call: a
// closure that captured one, a receiver that holds one, a function called by a
// handler, and a name that is a constant rather than a literal.
package main

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// archiveBucket is named by a constant rather than a literal, which is still
// something extraction can fold.
const archiveBucket = "archiveBucket"

type order struct {
	ID string `json:"id"`
}

// orders holds its resources on a receiver, which is the other way a handler
// is given one.
type orders struct {
	store datastore.Client
	work  queue.Client
}

func (o *orders) create(ctx context.Context, in order) (order, error) {
	if _, err := o.store.Put(ctx, datastore.Key{Partition: in.ID}, in); err != nil {
		return order{}, err
	}
	o.notify(ctx, in)
	return in, nil
}

// notify is reached only through create, so finding what it touches needs the
// walk to follow the call.
func (o *orders) notify(ctx context.Context, in order) {
	_, _ = o.work.Send(ctx, []byte(in.ID))
}

func main() {
	app := celerity.New()

	store := resources.Datastore(app, "ordersTable")
	work := resources.Queue(app, "workQueue")
	uploads := resources.Bucket(app, "uploadsBucket")
	archive := resources.Bucket(app, archiveBucket)
	events := resources.Topic(app, "orderEvents")

	service := &orders{store: store, work: work}
	celerity.Post(app, "/orders", service.create, celerity.Named("createOrder"))

	// A closure that captured one handle and nothing else.
	celerity.Get(app, "/uploads/{key}", func(ctx context.Context, _ struct{}) (int64, error) {
		info, err := uploads.Info(ctx, "a")
		return info.Size, err
	}, celerity.Named("getUpload"))

	// A closure reaching two, one of them by a constant name.
	celerity.Post(app, "/archive", func(ctx context.Context, _ struct{}) (struct{}, error) {
		_, err := uploads.Copy(ctx, "a", bucket.Destination{Key: "b", Store: archive})
		return struct{}{}, err
	}, celerity.Named("archiveUpload"))

	// A handler reaching nothing at all.
	celerity.Get(app, "/health", func(ctx context.Context, _ struct{}) (string, error) {
		return "ok", nil
	}, celerity.Named("health"))

	// A resource the walk cannot see, declared instead.
	celerity.Post(app, "/announce", func(ctx context.Context, _ struct{}) (struct{}, error) {
		return struct{}{}, announce(ctx, events)
	}, celerity.Named("announce"), celerity.Uses("auditBucket"))

	celerity.Run(app)
}

// announce takes its topic as an argument, which is a handle the walk follows
// into the function to find.
func announce(ctx context.Context, events topic.Client) error {
	_, err := events.Publish(ctx, []byte("announced"))
	return err
}
