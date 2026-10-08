// Provides an application whose handlers reach their resources indirectly.
//
// testdata/app covers the direct shapes: a closure that captured a handle, a
// receiver that holds one, a name that is a constant. These are the indirect
// ones, each of which was missed before it was here: a chain of services, a
// handle that only ever reaches a field as a constructor's parameter, and a
// service held behind an interface.
package main

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

type order struct {
	ID string `json:"id"`
}

// A chain: the handler calls a service, which calls another, and the handles
// are on each service's own receiver.

type orderStore struct{ items datastore.Client }

func (s *orderStore) save(ctx context.Context, in order) error {
	_, err := s.items.Put(ctx, datastore.Key{Partition: in.ID}, in)
	return err
}

type auditor struct{ log bucket.Store }

func (a *auditor) record(ctx context.Context, in order) {
	_, _ = a.log.Put(ctx, "audit/"+in.ID, nil)
}

type orders struct {
	store *orderStore
	audit *auditor
}

func (o *orders) create(ctx context.Context, in order) (order, error) {
	if err := o.store.save(ctx, in); err != nil {
		return order{}, err
	}
	o.audit.record(ctx, in)
	return in, nil
}

// A constructor: the field is assigned the parameter rather than the variable
// the caller named, so the handle is only connected by following the argument.

type shipping struct{ outbox queue.Client }

func newShipping(outbox queue.Client) *shipping { return &shipping{outbox: outbox} }

func (s *shipping) dispatch(ctx context.Context, in order) {
	_, _ = s.outbox.Send(ctx, []byte(in.ID))
}

// A factory answering with a closure: what the handler calls is a variable, and
// the body that captured the handle is inside the function that returned it.
func newPricer(sessions cache.Client) func(context.Context, order) error {
	return func(ctx context.Context, in order) error {
		_, err := sessions.Set(ctx, in.ID, "priced")
		return err
	}
}

// A factory called through a variable rather than by name, so the call site
// names neither the function nor a method on anything.
type reporter struct{ reports bucket.Store }

func newReporter(reports bucket.Store) *reporter { return &reporter{reports: reports} }

func (r *reporter) report(ctx context.Context, in order) error {
	_, err := r.reports.Put(ctx, "reports/"+in.ID, nil)
	return err
}

// A constructor that can fail, which is the ordinary shape where setting one up
// has a connection to establish, session credentials to obtain or similar. The
// result is two values, so one expression on the right and two names on the
// left, and what the handler reaches is what the call answered rather than a
// field it was given.
func openLedger(ledger bucket.Store) (bucket.Store, error) { return ledger, nil }

type ledgerKeeper struct{ ledger bucket.Store }

func (k *ledgerKeeper) write(ctx context.Context, in order) error {
	_, err := k.ledger.Put(ctx, "ledger/"+in.ID, nil)
	return err
}

// An interface: the call site names announcer, and what is behind it is known
// only from where the notifier was built.

type announcer interface {
	announce(ctx context.Context, in order)
}

type topicAnnouncer struct{ events topic.Client }

func (t *topicAnnouncer) announce(ctx context.Context, in order) {
	_, _ = t.events.Publish(ctx, []byte(in.ID))
}

type notifier struct{ out announcer }

func (n *notifier) notify(ctx context.Context, in order) { n.out.announce(ctx, in) }

func main() {
	app := celerity.New()

	items := resources.Datastore(app, "ordersTable")
	log := resources.Bucket(app, "auditBucket")
	outbox := resources.Queue(app, "shippingQueue")
	events := resources.Topic(app, "orderEvents")

	deep := &orders{store: &orderStore{items: items}, audit: &auditor{log: log}}
	celerity.Post(app, "/orders", deep.create, celerity.Named("createOrder"))

	ship := newShipping(outbox)
	celerity.Post(app, "/ship", func(ctx context.Context, in order) (order, error) {
		ship.dispatch(ctx, in)
		return in, nil
	}, celerity.Named("shipOrder"))

	price := newPricer(resources.Cache(app, "priceCache"))
	celerity.Post(app, "/price", func(ctx context.Context, in order) (order, error) {
		return in, price(ctx, in)
	}, celerity.Named("priceOrder"))

	build := newReporter
	reports := build(resources.Bucket(app, "reportBucket"))
	celerity.Post(app, "/report", func(ctx context.Context, in order) (order, error) {
		return in, reports.report(ctx, in)
	}, celerity.Named("reportOrder"))

	opened, err := openLedger(resources.Bucket(app, "ledgerBucket"))
	if err != nil {
		panic(err)
	}
	keeper := &ledgerKeeper{ledger: opened}
	celerity.Post(app, "/ledger", func(ctx context.Context, in order) (order, error) {
		return in, keeper.write(ctx, in)
	}, celerity.Named("writeLedger"))

	notify := &notifier{out: &topicAnnouncer{events: events}}
	celerity.Post(app, "/announce", func(ctx context.Context, in order) (order, error) {
		notify.notify(ctx, in)
		return in, nil
	}, celerity.Named("announceOrder"))

	celerity.Run(app)
}
