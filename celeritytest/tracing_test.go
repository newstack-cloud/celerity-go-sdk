package celeritytest_test

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// What a dispatch records, which is one span for the dispatch and one per
// resource operation under it. The names are the contract's rather than any
// provider's, so one trace reads the same whichever SDK or platform served it.
type TracingTestSuite struct {
	suite.Suite
}

func TestTracingTestSuite(t *testing.T) {
	suite.Run(t, new(TracingTestSuite))
}

func (s *TracingTestSuite) Test_a_dispatch_opens_a_span_naming_the_handler() {
	app, provider := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)
	s.Require().NoError(
		provider.DatastoreNamed("ordersTable").
			Seed(datastore.Key{Partition: "o-1"}, order{ID: "o-1", Total: 10}),
	)

	harness.GET(s.T(), "/orders/o-1").AssertStatus(s.T(), http.StatusOK)

	dispatch, found := tracer.Span("celerity.handler.http")
	s.Require().True(found, "every dispatch is traced, whatever kind it is")
	s.True(dispatch.Ended, "and the span is closed")

	name, ok := dispatch.Attr("handler.name")
	s.Require().True(ok)
	s.Equal("getOrder", name)

	route, ok := dispatch.Attr("http.route")
	s.Require().True(ok)
	s.Equal("/orders/{orderId}", route, "the route rather than the path, so one span covers all of them")
}

func (s *TracingTestSuite) Test_a_resource_operation_is_traced_under_the_dispatch() {
	app, _ := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-1", Total: 5})).
		AssertStatus(s.T(), http.StatusOK)

	send, found := tracer.Span("celerity.queue.send_message")
	s.Require().True(found)
	s.Equal("celerity.handler.http", send.Parent,
		"a resource span sits under the dispatch that reached it")

	resource, ok := send.Attr("queue.resource")
	s.Require().True(ok)
	s.Equal("workQueue", resource, "named as the blueprint names it, not as the deployment does")
}

func (s *TracingTestSuite) Test_a_failing_operation_records_what_failed_on_its_span() {
	app, res := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)
	refused := errors.New("the queue is full")
	res.QueueNamed("workQueue").Refuse = refused

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-1"}))

	send, found := tracer.Span("celerity.queue.send_message")
	s.Require().True(found)
	s.ErrorIs(send.Err, refused)
	s.True(send.Ended, "a span is ended whether the call worked or not")

	dispatch, found := tracer.Span("celerity.handler.http")
	s.Require().True(found)
	s.ErrorIs(dispatch.Err, refused, "and the dispatch failed with it")
}

func (s *TracingTestSuite) Test_a_send_records_how_large_the_message_was() {
	app, _ := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-1"}))

	send, found := tracer.Span("celerity.queue.send_message")
	s.Require().True(found)
	size, ok := send.Attr("queue.body_size")
	s.Require().True(ok)
	s.EqualValues(3, size, "o-1 is three bytes")
}

func (s *TracingTestSuite) Test_a_batch_records_what_became_of_every_entry() {
	// None of the counts are known before the call, so they are added to the
	// span that is already open rather than passed when it was started.
	app, _ := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)

	harness.POST(s.T(), "/orders/batch", celeritytest.JSONBody([]order{
		{ID: "o-1"}, {ID: "o-2"},
	})).AssertStatus(s.T(), http.StatusOK)

	batch, found := tracer.Span("celerity.queue.send_message_batch")
	s.Require().True(found)
	s.Equal("celerity.handler.http", batch.Parent)

	asked, ok := batch.Attr("queue.message_count")
	s.Require().True(ok)
	s.EqualValues(2, asked, "how many were handed over, known before the call")

	taken, ok := batch.Attr("queue.successful_count")
	s.Require().True(ok)
	s.EqualValues(2, taken, "and how many were taken, known only after it")

	unsent, ok := batch.Attr("queue.unsent_count")
	s.Require().True(ok)
	s.EqualValues(0, unsent)
}

func (s *TracingTestSuite) Test_a_consumer_dispatch_records_how_many_records_it_was_given() {
	built := buildKindsApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), built.app)

	harness.Consume(s.T(), "processOrders", []byte("one"), []byte("two"))

	dispatch, found := tracer.Span("celerity.handler.consumer")
	s.Require().True(found)
	size, ok := dispatch.Attr("handler.batch_size")
	s.Require().True(ok)
	s.EqualValues(2, size)
}

func (s *TracingTestSuite) Test_every_kind_of_dispatch_is_traced_under_its_own_name() {
	built := buildKindsApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), built.app)

	harness.Consume(s.T(), "processOrders", []byte("one"))
	harness.Schedule(s.T(), "nightlySweep")
	harness.Invoke(s.T(), "greet", greeting{Name: "ada"}, nil)
	harness.Send(s.T(), "sendMessage", []byte(`{"name":"ada"}`))

	s.Subset(tracer.Names(), []string{
		"celerity.handler.consumer",
		"celerity.handler.schedule",
		"celerity.handler.custom",
		"celerity.handler.websocket",
	})
}

func (s *TracingTestSuite) Test_an_application_that_did_not_install_a_tracer_still_runs() {
	telemetry.SetTracer(nil)
	app, res := buildApp()
	harness := celeritytest.New(s.T(), app)
	s.Require().NoError(res.DatastoreNamed("ordersTable").
		Seed(datastore.Key{Partition: "o-1"}, order{ID: "o-1"}))

	harness.GET(s.T(), "/orders/o-1").AssertStatus(s.T(), http.StatusOK)
}

func (s *TracingTestSuite) Test_the_recorder_is_removed_when_the_test_that_installed_it_ends() {
	// Otherwise one test's spans are another's, which with a process-wide
	// tracer is the thing to get wrong.
	var installed *celeritytest.Tracer
	s.Run("installs one", func() {
		installed = celeritytest.NewTracer(s.T())
		_, span := telemetry.CurrentTracer().Start(s.T().Context(), "inside")
		span.End()
		s.Equal([]string{"inside"}, installed.Names())
	})

	_, span := telemetry.CurrentTracer().Start(s.T().Context(), "after")
	span.End()

	s.Equal([]string{"inside"}, installed.Names(),
		"the span started after the subtest went to the no-op tracer")
}

func (s *TracingTestSuite) Test_a_bucket_operation_records_the_key_and_what_came_back() {
	app, res := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)
	_, err := res.BucketNamed("uploadsBucket").Put(
		s.T().Context(), "invoices/a.pdf", strings.NewReader("%PDF"))
	s.Require().NoError(err)

	harness.GET(s.T(), "/uploads/invoices/a.pdf").AssertStatus(s.T(), http.StatusOK)

	get, found := tracer.Span("celerity.bucket.get")
	s.Require().True(found)
	s.Equal("celerity.handler.http", get.Parent)

	key, ok := get.Attr("bucket.key")
	s.Require().True(ok)
	s.Equal("invoices/a.pdf", key)

	length, ok := get.Attr("bucket.content_length")
	s.Require().True(ok)
	s.EqualValues(4, length, "how much came back, known only after the call")
}

func (s *TracingTestSuite) Test_a_datastore_read_records_the_partition_but_not_the_item() {
	// A partition is a tenant and is what a trace is grouped by; a sort key is
	// usually one item's identifier and would be a high cardinality value on
	// every span.
	app, res := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)
	s.Require().NoError(res.DatastoreNamed("ordersTable").
		Seed(datastore.Key{Partition: "o-1"}, order{ID: "o-1"}))

	harness.GET(s.T(), "/orders/o-1").AssertStatus(s.T(), http.StatusOK)

	get, found := tracer.Span("celerity.datastore.get_item")
	s.Require().True(found, "named as the other SDKs name it, not after the Go method")

	partition, ok := get.Attr("datastore.partition")
	s.Require().True(ok)
	s.Equal("o-1", partition)

	_, hasSort := get.Attr("datastore.sort_key")
	s.False(hasSort, "the sort key itself is not recorded")
}

func (s *TracingTestSuite) Test_a_conditional_write_says_that_it_was_one() {
	// What separates a refusal worth retrying from a failure worth reporting.
	app, _ := buildApp()
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-1"}))

	put, found := tracer.Span("celerity.datastore.put_item")
	s.Require().True(found)
	conditional, ok := put.Attr("datastore.conditional")
	s.Require().True(ok)
	s.Equal(false, conditional, "this one was not")
}

func (s *TracingTestSuite) Test_a_generated_cache_operation_is_traced_like_the_rest() {
	// Every operation's delegation is covered in the resources package, against
	// the contract itself. What this adds is the path an application takes: a
	// handle resolved through the provider, traced, and reaching the double.
	res := celeritytest.Resources()
	app := celerity.New(celerity.WithResourceProvider(res))
	sessions := resources.Cache(app, "sessions")
	s.Require().NoError(app.Err())

	// Tracer is set as the global tracer read by the cache service.
	tracer := celeritytest.NewTracer(s.T())
	res.CacheNamed("sessions").GetFunc = func(
		ctx context.Context, key string,
	) (string, error) {
		return "v", nil
	}

	value, err := sessions.Get(s.T().Context(), "k")

	s.Require().NoError(err)
	s.Equal("v", value, "the wrapper delegates rather than answering itself")

	get, found := tracer.Span("celerity.cache.get")
	s.Require().True(found)
	key, ok := get.Attr("cache.key")
	s.Require().True(ok)
	s.Equal("k", key)
	s.True(get.Ended)
}

func (s *TracingTestSuite) Test_a_cache_walk_is_traced_as_one_span_over_the_whole_walk() {
	// A walk yields as it goes, so there is no one moment it finishes at. What
	// a caller wants to know is how long the walk took and how much it read.
	res := celeritytest.Resources()
	app := celerity.New(celerity.WithResourceProvider(res))
	sessions := resources.Cache(app, "sessions")
	s.Require().NoError(app.Err())

	tracer := celeritytest.NewTracer(s.T())
	res.CacheNamed("sessions").ScanFunc = func(
		ctx context.Context, opts ...cache.ScanOption,
	) iter.Seq2[string, error] {
		return func(yield func(string, error) bool) {
			for _, key := range []string{"a", "b", "c"} {
				if !yield(key, nil) {
					return
				}
			}
		}
	}

	var read []string
	for key, err := range sessions.Scan(s.T().Context()) {
		s.Require().NoError(err)
		read = append(read, key)
		if len(read) == 2 {
			break
		}
	}

	s.Equal([]string{"a", "b"}, read)

	walk, found := tracer.Span("celerity.cache.scan")
	s.Require().True(found)
	s.True(walk.Ended, "the span is ended even though the walk stopped early")

	count, ok := walk.Attr("cache.key_count")
	s.Require().True(ok)
	s.EqualValues(2, count, "what was read rather than what there was")

	stopped, ok := walk.Attr("cache.stopped_early")
	s.Require().True(ok)
	s.Equal(true, stopped)
}

func (s *TracingTestSuite) Test_a_log_a_handler_wrote_names_the_trace_it_belongs_to() {
	var written []map[string]any
	app, res := buildAppWithLogger(&written)
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), app)
	s.Require().NoError(res.DatastoreNamed("ordersTable").
		Seed(datastore.Key{Partition: "o-1"}, order{ID: "o-1"}))

	harness.GET(s.T(), "/orders/o-1").AssertStatus(s.T(), http.StatusOK)

	dispatch, found := tracer.Span("celerity.handler.http")
	s.Require().True(found)
	s.Require().NotEmpty(written, "the handler wrote a record")

	record := written[0]
	s.Equal(dispatch.TraceID, record["trace_id"], "the record names the dispatch's trace")
	s.Equal(dispatch.SpanID, record["span_id"])
	s.Equal("getOrder", record["handler"], "alongside what was already bound")
}

func (s *TracingTestSuite) Test_a_record_carries_no_trace_where_nothing_is_recording() {
	// Rather than empty strings a query would have to know to ignore.
	telemetry.SetTracer(nil)
	var written []map[string]any
	app, res := buildAppWithLogger(&written)
	harness := celeritytest.New(s.T(), app)
	s.Require().NoError(res.DatastoreNamed("ordersTable").
		Seed(datastore.Key{Partition: "o-1"}, order{ID: "o-1"}))

	harness.GET(s.T(), "/orders/o-1").AssertStatus(s.T(), http.StatusOK)

	s.Require().NotEmpty(written)
	_, hasTrace := written[0]["trace_id"]
	s.False(hasTrace)
	s.Equal("getOrder", written[0]["handler"], "while what is known is still bound")
}

// pricedApp is an application whose handler opens a span of its own, which is
// the telemetry worth asserting: the SDK's spans are the SDK's behaviour.
func pricedApp() *celerity.App {
	app := celerity.New(celerity.WithResourceProvider(celeritytest.Resources()))
	celerity.Post(app, "/quotes", func(ctx context.Context, in order) (order, error) {
		return telemetry.Traced(ctx, "orders.price_quote",
			[]telemetry.Attr{telemetry.String("order.id", in.ID)},
			func(_ context.Context, span telemetry.Span) (order, error) {
				in.Total = in.Total * 2
				span.SetAttributes(telemetry.Int("order.total", in.Total))
				return in, nil
			})
	}, celerity.Named("quote"))
	return app
}

func (s *TracingTestSuite) Test_assert_span_returns_a_span_the_application_opened() {
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), pricedApp())

	harness.POST(s.T(), "/quotes", celeritytest.JSONBody(order{ID: "o-1", Total: 10})).
		AssertStatus(s.T(), http.StatusOK)

	span := tracer.AssertSpan(s.T(), "orders.price_quote")

	id, recorded := span.Attr("order.id")
	s.True(recorded)
	s.Equal("o-1", id)

	total, recorded := span.Attr("order.total")
	s.True(recorded, "an attribute set inside the call, not only the ones passed in")
	s.Equal(int64(20), total)
	s.True(span.Ended)
	s.Equal("celerity.handler.http", span.Parent, "under the dispatch")
}

func (s *TracingTestSuite) Test_assert_span_reports_what_was_recorded_when_a_name_is_missing() {
	// A span that is missing is usually one under a different name, so the
	// names that were recorded are the useful thing to say.
	tracer := celeritytest.NewTracer(s.T())
	harness := celeritytest.New(s.T(), pricedApp())
	harness.POST(s.T(), "/quotes", celeritytest.JSONBody(order{ID: "o-1"})).
		AssertStatus(s.T(), http.StatusOK)

	failed := failureOf(func(tb testing.TB) {
		tracer.AssertSpan(tb, "orders.price_quotes")
	})

	s.Require().True(failed.stopped)
	s.Contains(failed.message, `no span was recorded as "orders.price_quotes"`)
	s.Contains(failed.message, "orders.price_quote", "and the one that was, so a typo is obvious")
}

func (s *TracingTestSuite) Test_a_second_recorder_is_refused_rather_than_collecting_both() {
	// The seam is process-wide, so two recorders at once means each reads spans
	// the other's test produced. Which is what a test calling t.Parallel would
	// get, silently, before this was refused.
	_ = celeritytest.NewTracer(s.T())

	failed := failureOf(func(tb testing.TB) {
		celeritytest.NewTracer(tb)
	})

	s.Require().True(failed.stopped)
	s.Contains(failed.message, "already installed")
	s.Contains(failed.message, "t.Parallel", "naming the way a suite hits it")
}
