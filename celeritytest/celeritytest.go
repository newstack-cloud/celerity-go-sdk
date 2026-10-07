// Package celeritytest runs an application's handlers in a test, without a
// runtime, a network or a cloud account.
//
// A handler is dispatched through the application's own pipeline, so the
// layers, guards and context a deployment would apply are the ones a test
// exercises:
//
//	func TestCreatingAnOrder(t *testing.T) {
//	    res := celeritytest.Resources()
//	    harness := celeritytest.New(t, orders.App(celerity.WithResourceProvider(res)))
//
//	    harness.POST(t, "/orders", celeritytest.JSONBody(order{Total: 10})).
//	        AssertStatus(t, http.StatusCreated)
//	}
//
// The application is built by the application's own constructor, not rebuilt by
// the test.
//
// There are two kinds of dependency and they are substituted in two
// different places. Firstly, what the application itself depends on, a payment gateway
// or a clock, arrives through the constructor the application wrote, as an
// argument or an option of its own. Secondly, what Celerity provides, the resources a
// blueprint declares, is substituted here: one [celerity.WithResourceProvider]
// redirects all of them at once, and nothing else about the application
// changes.
//
// Every dispatch takes the test it belongs to rather than the one the
// application was built with, so a harness built once in a parent test is used
// from subtests that each have their own.
//
// # Resources
//
// A handler reaching a bucket, a queue, a topic or a data store is given a
// working one held in memory, so a write followed by a read answers with what
// was written. A cache is a recording stub, since reimplementing a surface that
// large for in-memory testing is a large undertaking and you can get all the information
// you need from call assertions on a stub.
//
// A SQL database has neither. What a handler has to be right about is the
// statement it sends, and only an engine can judge that, so a database a test
// did not supply refuses the call and says where to get one.
// [Provider.WithDatabase] takes the engine a suite brought up, which in a
// development session is the one celerity dev already runs.
//
// # Live resources
//
// [Live] is for the integration cases, resources served by what a `celerity dev test`
// session brought up, reached through the provider the build linked rather than
// through anything here. A handler then runs against a real engine, a real
// object store and a real cache, while still being dispatched in process
// through the application's own pipeline, which is a smaller and faster test
// than one driving the runtime container.
//
// The two mix per resource. Asking for a double by name before the application
// takes its handles substitutes it for that one resource, so a suite can
// exercise a real database and keep the queue in memory.
package celeritytest

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// App dispatches events to an application's handlers.
type App struct {
	app       *celerity.App
	resources *Provider
	now       func() time.Time
	deadline  time.Duration
	traceCtx  map[string]string

	// Counted rather than random, so that a failure names the same event on
	// every run and two runs of a suite can be compared.
	ids atomic.Uint64
}

// nextID names one dispatch, or one of the things a dispatch carries.
func (a *App) nextID(prefix string) string {
	return prefix + "-" + strconv.FormatUint(a.ids.Add(1), 10)
}

// Option configures a test application.
type Option func(*App)

// WithClock fixes the time events are stamped with, for a test asserting on it.
func WithClock(now func() time.Time) Option {
	return func(a *App) {
		a.now = now
	}
}

// WithDeadline sets how long after an event's timestamp its deadline falls.
//
// The deadline is applied to the handler's context the way a runtime applies
// one, so a handler that checks for cancellation can be tested against it.
func WithDeadline(d time.Duration) Option {
	return func(a *App) {
		a.deadline = d
	}
}

// WithTraceContext gives every dispatch the W3C trace context an event would
// have arrived with.
func WithTraceContext(tc map[string]string) Option {
	return func(a *App) {
		a.traceCtx = tc
	}
}

// defaultDeadline is what a dispatch is given where the test doesn't provide one.
// Long enough that a handler doing ordinary work is not cut short,
// short enough that a handler waiting on something that will
// never arrive fails the test rather than hanging it.
const defaultDeadline = 30 * time.Second

// New returns an application ready to dispatch to, failing the test where
// registration did not succeed.
//
// Registration errors are collected rather than returned, so an application
// that named a resource no provider can build, or registered two handlers under
// one tag, reports it here rather than on the first dispatch.
func New(t testing.TB, app *celerity.App, opts ...Option) *App {
	t.Helper()

	a := &App{
		app:      app,
		now:      time.Now,
		deadline: defaultDeadline,
	}
	for _, opt := range opts {
		opt(a)
	}

	if err := app.Err(); err != nil {
		t.Fatalf("celeritytest: the application did not finish registering:\n%v", err)
	}
	if app.Registry().Len() == 0 {
		t.Fatal("celeritytest: the application registered no handlers")
	}
	return a
}

// NewWithResources returns an application whose handlers reach the test
// doubles in p, and the doubles themselves for a test to read afterwards.
//
// The provider has to be given to [celerity.New] as well, since handles are
// taken while the application is being built:
//
//	res := celeritytest.Resources()
//	harness := celeritytest.NewWithResources(
//	    t, orders.App(celerity.WithResourceProvider(res)), res)
func NewWithResources(t testing.TB, app *celerity.App, p *Provider, opts ...Option) *App {
	t.Helper()

	a := New(t, app, opts...)
	a.resources = p
	return a
}

// Resources returns the test doubles an application reaches through
// [celerity.WithResourceProvider].
func (a *App) Resources() *Provider {
	return a.resources
}

// dispatch runs one event through the registration's own pipeline.
//
// A handler that returned an error is an outcome rather than a fault in the
// test, so the error is handed back for the caller to report or assert on. Only
// a dispatch that produced neither a result nor an error is a fault, since
// there is nothing a test could do with that.
func (a *App) dispatch(
	t testing.TB, reg *celerity.Registration, ev *handler.Event,
) (*handler.Result, error) {
	t.Helper()

	ctx, cancel := context.WithDeadline(context.Background(), ev.Deadline)
	defer cancel()

	result, err := a.app.Pipeline(reg)(ctx, ev)
	if err != nil {
		return result, err
	}
	if result == nil {
		t.Fatalf("celeritytest: %s answered with no result and no error", reg.Name)
	}
	return result, nil
}

// event builds the envelope every dispatch shares.
func (a *App) event(reg *celerity.Registration, id string) *handler.Event {
	now := a.now()
	return &handler.Event{
		ID:           id,
		Tag:          reg.Tag,
		Kind:         reg.Kind,
		Timestamp:    now,
		Deadline:     now.Add(a.deadline),
		TraceContext: a.traceCtx,
	}
}
