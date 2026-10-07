package celeritytest_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Live resources are the services a `celerity dev test` session brought up,
// reached through the provider the build linked.
//
// Nothing real is reached here, and these are not integration tests: what they
// assert is the routing, which of the two a handler was given, and that
// substituting a double is refused once it is too late to take effect. So the
// provider is a stand-in, and whether its answers reach a service is its own
// business rather than something this package decides.
type LiveTestSuite struct {
	suite.Suite
}

func TestLiveTestSuite(t *testing.T) {
	suite.Run(t, new(LiveTestSuite))
}

func (s *LiveTestSuite) Test_a_name_no_double_was_registered_for_is_routed_to_the_linked_provider() {
	linked := newLinkedProvider()
	res := celeritytest.LiveWith(s.T(), linked)
	harness := celeritytest.New(s.T(), ordersApp(res))

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-1"})).
		AssertStatus(s.T(), http.StatusOK)

	// Read back from the stand-in's own store rather than the harness's.
	// The write went to the provider rather than to a
	// double made under that name.
	var stored order
	_, err := linked.datastore("ordersTable").Get(
		s.T().Context(), datastore.Key{Partition: "o-1"}, &stored)
	s.Require().NoError(err)
	s.Equal("o-1", stored.ID)
}

func (s *LiveTestSuite) Test_a_double_asked_for_first_is_used_instead_of_the_linked_provider() {
	// The mixing case, as the routing sees it, the data store resolves through
	// the provider while the queue resolves to a double. In a suite that is a
	// real store and an in-memory queue; here both are doubles, and which one
	// answered is the point.
	linked := newLinkedProvider()
	res := celeritytest.LiveWith(s.T(), linked)

	work := res.QueueNamed("workQueue")
	harness := celeritytest.New(s.T(), ordersApp(res))

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-1"})).
		AssertStatus(s.T(), http.StatusOK)

	sent := work.Sent()
	s.Require().Len(sent, 1, "the double took the send")
	s.Equal("o-1", sent[0].Text())
	s.Empty(linked.queue("workQueue").Sent(), "and the linked provider was never asked")

	var stored order
	_, err := linked.datastore("ordersTable").Get(
		s.T().Context(), datastore.Key{Partition: "o-1"}, &stored)
	s.Require().NoError(err, "while the store still resolved through the provider")
}

func (s *LiveTestSuite) Test_a_double_asked_for_too_late_is_refused() {
	// A handle is taken during registration and held, so a double made after
	// that is one nothing will reach. Silently returning it would make the test
	// read an empty double and conclude the handler did nothing.
	linked := newLinkedProvider()

	failed := failureOf(func(tb testing.TB) {
		res := celeritytest.LiveWith(tb, linked)
		// Registration happens in ordersApp instantiation.
		_ = ordersApp(res)
		res.QueueNamed("workQueue")
	})

	s.Require().True(failed.stopped)
	s.Contains(failed.message, "already took a handle for queue \"workQueue\"")
	s.Contains(failed.message, "before building the application")
}

func (s *LiveTestSuite) Test_the_order_does_not_matter_where_everything_is_a_double() {
	// Resources() has one object per name either way, so the guard is armed
	// only where a provider is delegated to, and must not fire here.
	res := celeritytest.Resources()
	_ = ordersApp(res)

	s.NotNil(res.QueueNamed("workQueue"), "asked for after registration, which is fine")
}

func (s *LiveTestSuite) Test_the_linked_provider_is_released_once_however_often_it_is_asked() {
	linked := newLinkedProvider()
	res := celeritytest.LiveWith(s.T(), linked)

	s.Require().NoError(res.Close(s.T().Context()))
	s.Require().NoError(res.Close(s.T().Context()), "a second close is not a second release")

	s.Equal(1, linked.closes, "whatever the provider holds is given back once")
}

func (s *LiveTestSuite) Test_Live_says_what_to_import_where_no_provider_is_linked() {
	// The failure a suite hits first, so it names the imports rather than
	// reporting that nothing was detected.
	failed := failureOf(func(tb testing.TB) { celeritytest.Live(tb) })

	s.Require().True(failed.stopped)
	s.Contains(failed.message, "no resource provider is linked")
	s.Contains(failed.message, "resources/local")
	s.Contains(failed.message, "CELERITY_PLATFORM=local")
}

func (s *LiveTestSuite) Test_Live_says_to_set_the_platform_where_providers_are_linked() {
	// The second mistake, and the one a suite hits after fixing the first: with
	// more than one linked, which serves is the platform's answer. Reporting
	// this as nothing being linked would send a reader back to the imports they
	// just added.
	registered := celeritytest.NotDetectedMessage([]string{"aws", "local"})

	s.Contains(registered, "none of them serves this platform")
	s.Contains(registered, "CELERITY_PLATFORM=local")
	s.Contains(registered, "aws, local", "and which are linked")
	s.NotContains(registered, "import (", "not the imports, which are not the problem")
}

// ordersApp writes through a data store and a queue, which is enough to tell
// apart what came from the linked provider and what came from a double.
func ordersApp(res *celeritytest.Provider) *celerity.App {
	app := celerity.New(celerity.WithResourceProvider(res))

	orders := resources.Datastore(app, "ordersTable")
	work := resources.Queue(app, "workQueue")

	celerity.Post(app, "/orders", func(ctx context.Context, in order) (order, error) {
		if _, err := orders.Put(ctx, datastore.Key{Partition: in.ID}, in); err != nil {
			return order{}, err
		}
		if _, err := work.Send(ctx, []byte(in.ID)); err != nil {
			return order{}, err
		}
		return in, nil
	}, celerity.Named("createOrder"))

	return app
}

// linkedProvider stands in for the provider a development session links.
// What it hands back are this package's own doubles, kept
// separate from the harness's so a test can tell which of the two answered.
type linkedProvider struct {
	datastores map[string]*celeritytest.Datastore
	queues     map[string]*celeritytest.Queue
	buckets    map[string]*celeritytest.Bucket
	topics     map[string]*celeritytest.Topic
	closes     int
}

func newLinkedProvider() *linkedProvider {
	return &linkedProvider{
		datastores: map[string]*celeritytest.Datastore{},
		queues:     map[string]*celeritytest.Queue{},
		buckets:    map[string]*celeritytest.Bucket{},
		topics:     map[string]*celeritytest.Topic{},
	}
}

func (p *linkedProvider) Name() string { return "linked" }

func (p *linkedProvider) datastore(name string) *celeritytest.Datastore {
	if existing, ok := p.datastores[name]; ok {
		return existing
	}
	p.datastores[name] = celeritytest.NewDatastore(name)
	return p.datastores[name]
}

func (p *linkedProvider) queue(name string) *celeritytest.Queue {
	if existing, ok := p.queues[name]; ok {
		return existing
	}
	p.queues[name] = celeritytest.NewQueue(name)
	return p.queues[name]
}

func (p *linkedProvider) Datastore(ref resources.Ref) (datastore.Client, error) {
	return p.datastore(ref.Name), nil
}

func (p *linkedProvider) Queue(ref resources.Ref) (queue.Client, error) {
	return p.queue(ref.Name), nil
}

func (p *linkedProvider) Bucket(ref resources.Ref) (bucket.Store, error) {
	if _, ok := p.buckets[ref.Name]; !ok {
		p.buckets[ref.Name] = celeritytest.NewBucket(ref.Name)
	}
	return p.buckets[ref.Name], nil
}

func (p *linkedProvider) Topic(ref resources.Ref) (topic.Client, error) {
	if _, ok := p.topics[ref.Name]; !ok {
		p.topics[ref.Name] = celeritytest.NewTopic(ref.Name)
	}
	return p.topics[ref.Name], nil
}

func (p *linkedProvider) Cache(ref resources.Ref) (cache.Client, error) {
	return celeritytest.NewCacheStub(ref.Name), nil
}

func (p *linkedProvider) SQLDatabase(resources.Ref) (sqldb.Client, error) {
	return nil, errors.New("the stand-in has no database")
}

func (p *linkedProvider) Close(context.Context) error {
	p.closes++
	return nil
}
