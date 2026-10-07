package celeritytest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

type HarnessTestSuite struct {
	suite.Suite
}

func TestHarnessTestSuite(t *testing.T) {
	suite.Run(t, new(HarnessTestSuite))
}

// order is what the application under test deals in.
type order struct {
	ID    string `json:"id"`
	Total int    `json:"total"`
}

type orderPath struct {
	ID string `json:"-" path:"orderId"`
}

type uploadPath struct {
	Path string `json:"-" path:"filePath"`
}

type upload struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

// buildApp is an application of the shape a handler author writes. Handlers
// close over the resources they reach, which is how a dependency arrives
// without a container.
func buildApp() (*celerity.App, *celeritytest.Provider) {
	res := celeritytest.Resources()
	app := celerity.New(celerity.WithResourceProvider(res))

	orders := resources.Datastore(app, "ordersTable")
	work := resources.Queue(app, "workQueue")
	uploads := resources.Bucket(app, "uploadsBucket")

	celerity.Get(app, "/orders/{orderId}",
		func(ctx context.Context, in orderPath) (order, error) {
			var found order
			if _, err := orders.Get(ctx, datastore.Key{Partition: in.ID}, &found); err != nil {
				return order{}, err
			}
			return found, nil
		}, celerity.Named("getOrder"))

	celerity.Post(app, "/orders",
		func(ctx context.Context, in order) (order, error) {
			if _, err := orders.Put(ctx, datastore.Key{Partition: in.ID}, in); err != nil {
				return order{}, err
			}
			if _, err := work.Send(ctx, []byte(in.ID)); err != nil {
				return order{}, err
			}
			return in, nil
		}, celerity.Named("createOrder"))

	celerity.Post(app, "/orders/batch",
		func(ctx context.Context, in []order) (map[string]int, error) {
			entries := make([]queue.BatchEntry, 0, len(in))
			for _, o := range in {
				entries = append(entries, queue.BatchEntry{ID: o.ID, Body: []byte(o.ID)})
			}
			result, err := work.SendBatch(ctx, entries)
			if err != nil {
				return nil, err
			}
			return map[string]int{"sent": len(result.Successful)}, nil
		}, celerity.Named("createOrders"))

	celerity.Get(app, "/uploads/{filePath+}",
		func(ctx context.Context, in uploadPath) (upload, error) {
			object, err := uploads.Get(ctx, in.Path)
			if err != nil {
				return upload{}, err
			}
			defer object.Body.Close()
			return upload{Key: in.Path, Type: object.Info.ContentType}, nil
		}, celerity.Named("getUpload"))

	return app, res
}

func (s *HarnessTestSuite) Test_a_handler_answers_a_request_through_the_real_pipeline() {
	app, res := buildApp()
	harness := celeritytest.New(s.T(), app)
	s.Require().NoError(res.DatastoreNamed("ordersTable").
		Seed(datastore.Key{Partition: "o-1"}, order{ID: "o-1", Total: 10}))

	response := harness.GET(s.T(), "/orders/o-1")

	response.AssertStatus(s.T(), http.StatusOK)
	var got order
	response.Decode(s.T(), &got)
	s.Equal(order{ID: "o-1", Total: 10}, got)
}

func (s *HarnessTestSuite) Test_a_write_a_handler_made_is_there_to_read() {
	// What makes a working double worth having over a stub: the handler writes
	// and the test reads, without the test arranging the read.
	app, res := buildApp()
	harness := celeritytest.New(s.T(), app)

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-2", Total: 25})).
		AssertStatus(s.T(), http.StatusOK)

	store := res.DatastoreNamed("ordersTable")
	s.Equal(1, store.Len())

	var stored order
	_, err := store.Get(context.Background(), datastore.Key{Partition: "o-2"}, &stored)
	s.Require().NoError(err)
	s.Equal(25, stored.Total)
}

func (s *HarnessTestSuite) Test_what_a_handler_sent_is_what_a_test_reads() {
	app, res := buildApp()
	harness := celeritytest.New(s.T(), app)

	harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-3", Total: 5}))

	sent := res.QueueNamed("workQueue").Sent()
	s.Require().Len(sent, 1)
	s.Equal("o-3", sent[0].Text())
}

func (s *HarnessTestSuite) Test_a_handler_that_failed_reports_what_it_failed_with() {
	// Nothing was seeded, so the read finds nothing and the handler returns the
	// absence rather than answering.
	app, _ := buildApp()
	harness := celeritytest.New(s.T(), app)

	response := harness.GET(s.T(), "/orders/missing")

	s.Require().Error(response.Error())
	s.Contains(response.Error().Error(), "not found")
}

func (s *HarnessTestSuite) Test_a_catch_all_route_binds_every_segment() {
	app, res := buildApp()
	harness := celeritytest.New(s.T(), app)
	_, err := res.BucketNamed("uploadsBucket").Put(
		context.Background(), "invoices/2026/01.pdf", strings.NewReader("%PDF"),
		func(o *bucket.PutOptions) {
			o.ContentType = "application/pdf"
		})
	s.Require().NoError(err)

	response := harness.GET(s.T(), "/uploads/invoices/2026/01.pdf")

	response.AssertStatus(s.T(), http.StatusOK)
	var got upload
	response.Decode(s.T(), &got)
	s.Equal("invoices/2026/01.pdf", got.Key, "every segment, as the handler asked for it")
	s.Equal("application/pdf", got.Type)
}

func (s *HarnessTestSuite) Test_a_queue_that_will_not_take_a_message_surfaces_as_a_failure() {
	app, res := buildApp()
	harness := celeritytest.New(s.T(), app)
	res.QueueNamed("workQueue").Refuse = errors.New("the queue is full")

	response := harness.POST(s.T(), "/orders", celeritytest.JSONBody(order{ID: "o-4"}))

	s.Require().Error(response.Error())
	s.Contains(response.Error().Error(), "the queue is full")
}

func (s *HarnessTestSuite) Test_the_same_handle_is_handed_to_the_application_and_the_test() {
	// A test reads what a handler wrote only if the two hold one object, which
	// is what the provider keeping them by name is for.
	app, res := buildApp()
	_ = celeritytest.New(s.T(), app)

	first := res.QueueNamed("workQueue")
	second, err := res.Queue(resources.Ref{Kind: resources.KindQueue, Name: "workQueue"})

	s.Require().NoError(err)
	s.Same(first, second)
}

func (s *HarnessTestSuite) Test_a_path_no_handler_serves_names_what_is_registered() {
	app, _ := buildApp()
	harness := celeritytest.New(s.T(), app)

	failed := failureOf(func(tb testing.TB) {
		harness.GET(tb, "/nothing-here")
	})

	s.Require().True(failed.stopped, "the harness should stop the test rather than carry on")
	s.Contains(failed.message, "no handler is registered for GET /nothing-here")
	s.Contains(failed.message, "/orders/{orderId}", "and what is, so a typo is obvious")
}

// failureOf runs something that is expected to fail its test, and reports what
// it said. The harness stops a test the way testing.TB does, by not returning,
// so the stand-in unwinds with a panic and this catches it.
func failureOf(run func(testing.TB)) (recorder *recordingTB) {
	recorder = &recordingTB{}
	defer func() {
		recorder.runCleanups()
		r := recover()
		if r == nil {
			return
		}
		if err, ok := r.(error); !ok || !errors.Is(err, errStopped) {
			panic(r)
		}
	}()
	run(recorder)
	return recorder
}

type recordingTB struct {
	testing.TB
	stopped  bool
	message  string
	cleanups []func()
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Cleanup(fn func()) {
	r.cleanups = append(r.cleanups, fn)
}

// runCleanups runs what was registered, last first, the way testing.TB does.
func (r *recordingTB) runCleanups() {
	for i := len(r.cleanups) - 1; i >= 0; i-- {
		r.cleanups[i]()
	}
	r.cleanups = nil
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.stopped = true
	r.message = fmt.Sprintf(format, args...)
	panic(errStopped)
}

func (r *recordingTB) Fatal(args ...any) {
	r.stopped = true
	r.message = fmt.Sprint(args...)
	panic(errStopped)
}

var errStopped = errors.New("celeritytest: the harness stopped the test")

// buildAppWithLogger is buildApp with a handler that writes a record, and the
// records it wrote decoded for a test to read.
func buildAppWithLogger(written *[]map[string]any) (*celerity.App, *celeritytest.Provider) {
	res := celeritytest.Resources()
	app := celerity.New(
		celerity.WithResourceProvider(res),
		celerity.WithLogger(
			slog.New(
				slog.NewJSONHandler(&recordingWriter{written: written}, nil),
			),
		),
	)

	orders := resources.Datastore(app, "ordersTable")
	celerity.Get(app, "/orders/{orderId}",
		func(ctx context.Context, in orderPath) (order, error) {
			telemetry.LoggerFrom(ctx).Info("reading an order")
			var found order
			if _, err := orders.Get(ctx, datastore.Key{Partition: in.ID}, &found); err != nil {
				return order{}, err
			}
			return found, nil
		}, celerity.Named("getOrder"))

	return app, res
}

// recordingWriter decodes the records a handler wrote, which is what a test
// asserts on rather than the bytes.
type recordingWriter struct {
	written *[]map[string]any
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	var record map[string]any
	if err := json.Unmarshal(p, &record); err != nil {
		return 0, err
	}
	*w.written = append(*w.written, record)
	return len(p), nil
}
