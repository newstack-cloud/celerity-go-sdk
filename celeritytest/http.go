package celeritytest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	celerityhandler "github.com/newstack-cloud/celerity-go-sdk/handler"
)

// RequestOption configures a request before it is dispatched.
type RequestOption func(*celerityhandler.Request)

// Body sends raw bytes, leaving the content type to the caller.
func Body(body []byte) RequestOption {
	return func(r *celerityhandler.Request) {
		r.Body = body
	}
}

// JSONBody encodes a value as the request body and declares it as JSON.
//
// A value that cannot be encoded is sent as no body at all, which the handler
// will reject: a test that got here has a fault in what it is sending rather
// than in what it is testing, and [App.Do] reports it.
func JSONBody(v any) RequestOption {
	return func(r *celerityhandler.Request) {
		encoded, err := json.Marshal(v)
		if err != nil {
			r.Body = nil
			r.Headers.Add(badBodyHeader, err.Error())
			return
		}
		r.Body = encoded
		r.Headers.Add("content-type", "application/json")
	}
}

// badBodyHeader carries an encoding failure from [JSONBody] to the dispatch
// that can report it, since an option cannot fail on its own.
const badBodyHeader = "celeritytest-bad-body"

// Header adds a request header. Names are lowercased, as a handler sees them.
func Header(name, value string) RequestOption {
	return func(r *celerityhandler.Request) {
		r.Headers.Add(strings.ToLower(name), value)
	}
}

// Query adds a query parameter.
func Query(name, value string) RequestOption {
	return func(r *celerityhandler.Request) {
		r.QueryParams.Add(name, value)
	}
}

// SourceIP sets the address the request appears to come from, which a guard or
// a rate limiter may read.
func SourceIP(ip string) RequestOption {
	return func(r *celerityhandler.Request) {
		r.SourceIP = ip
	}
}

// GET dispatches a GET request to the handler registered for the path.
func (a *App) GET(t testing.TB, path string, opts ...RequestOption) *Response {
	t.Helper()
	return a.Do(t, http.MethodGet, path, opts...)
}

// POST dispatches a POST request.
func (a *App) POST(t testing.TB, path string, opts ...RequestOption) *Response {
	t.Helper()
	return a.Do(t, http.MethodPost, path, opts...)
}

// PUT dispatches a PUT request.
func (a *App) PUT(t testing.TB, path string, opts ...RequestOption) *Response {
	t.Helper()
	return a.Do(t, http.MethodPut, path, opts...)
}

// PATCH dispatches a PATCH request.
func (a *App) PATCH(t testing.TB, path string, opts ...RequestOption) *Response {
	t.Helper()
	return a.Do(t, http.MethodPatch, path, opts...)
}

// DELETE dispatches a DELETE request.
func (a *App) DELETE(t testing.TB, path string, opts ...RequestOption) *Response {
	t.Helper()
	return a.Do(t, http.MethodDelete, path, opts...)
}

// Do dispatches a request of any method to the handler registered for the path.
//
// The path is the one a client would send rather than the route a handler was
// registered under, so the parameters in it are bound the way a runtime binds
// them. A path no handler is registered for fails the test, since a request
// that reached nothing is a mistake in the test rather than a 404 worth
// asserting on.
func (a *App) Do(
	t testing.TB, method, path string, opts ...RequestOption,
) *Response {
	t.Helper()

	request := &celerityhandler.Request{
		Method:      strings.ToUpper(method),
		Path:        path,
		PathParams:  celerityhandler.Params{},
		QueryParams: celerityhandler.Params{},
		Headers:     celerityhandler.Params{},
		RequestID:   a.nextID("request"),
	}
	for _, opt := range opts {
		opt(request)
	}
	if bad := request.Headers.Get(badBodyHeader); bad != "" {
		t.Fatalf("celeritytest: encoding the request body: %s", bad)
	}

	reg, params := a.routeFor(t, request.Method, path)
	request.Route = reg.Route
	request.PathParams = params

	event := a.event(reg, a.nextID("event"))
	event.HTTP = request

	result, err := a.dispatch(t, reg, event)
	return &Response{Result: result, Handler: reg.Name, failed: err}
}

// Finds the handler a request reaches, reporting what is registered
// when nothing matches, a path that reached nothing is usually a typo, and the
// list is what makes that obvious.
func (a *App) routeFor(
	t testing.TB, method, path string,
) (*celerity.Registration, celerityhandler.Params) {
	t.Helper()

	var registered []string
	for _, reg := range a.app.Registry().OfKind(celerityhandler.KindHTTP) {
		registered = append(registered, reg.Method+" "+reg.Route)
		if reg.Method != method {
			continue
		}
		if params, ok := matchRoute(reg.Route, path); ok {
			return reg, params
		}
	}

	t.Fatalf("celeritytest: no handler is registered for %s %s.\nRegistered:\n\t%s",
		method, path, strings.Join(registered, "\n\t"))
	return nil, nil
}

// Response is what an HTTP handler answered.
type Response struct {
	// Result is the whole answer, for an assertion the helpers do not cover.
	// Nil where the handler failed before producing one.
	Result *celerityhandler.Result
	// Handler is the blueprint name of the handler that answered, named in
	// failures so that a test says which one disappointed it.
	Handler string

	// failed is what the handler returned, which the pipeline hands back as an
	// error rather than as a result.
	failed error
}

// Status is the status the handler answered with, and zero where it failed
// instead of answering.
func (r *Response) Status() int {
	if r.Result == nil || r.Result.HTTP == nil {
		return 0
	}
	return r.Result.HTTP.Status
}

// Body is the raw response body.
func (r *Response) Body() []byte {
	if r.Result == nil || r.Result.HTTP == nil {
		return nil
	}
	return r.Result.HTTP.Body
}

// Header returns the first value of a response header.
func (r *Response) Header(name string) string {
	if r.Result == nil || r.Result.HTTP == nil {
		return ""
	}
	return r.Result.HTTP.Headers.Get(strings.ToLower(name))
}

// AssertStatus fails the test unless the handler answered with want.
//
// The body is reported on a mismatch, since the usual cause is an error the
// handler described there.
func (r *Response) AssertStatus(t testing.TB, want int) *Response {
	t.Helper()

	if err := r.failure(); err != nil {
		t.Fatalf("celeritytest: %s failed instead of answering %d: %v", r.Handler, want, err)
	}
	if got := r.Status(); got != want {
		t.Fatalf("celeritytest: %s answered %d, wanted %d\nbody: %s",
			r.Handler, got, want, r.Body())
	}
	return r
}

// Decode reads a JSON response body into out, failing the test where it is not
// the shape asked for.
func (r *Response) Decode(t testing.TB, out any) {
	t.Helper()

	if err := r.failure(); err != nil {
		t.Fatalf("celeritytest: %s failed instead of answering: %v", r.Handler, err)
	}
	if err := json.Unmarshal(r.Body(), out); err != nil {
		t.Fatalf("celeritytest: reading %s's answer as %T: %v\nbody: %s",
			r.Handler, out, err, r.Body())
	}
}

// Error is the failure the handler reported, and nil where it answered
// normally.
//
// A handler that returns an error does not answer with a status: turning one
// into a response is the runtime's job rather than the handler's, so this is
// what a test asserting on a failure reads.
func (r *Response) Error() error {
	return r.failure()
}

func (r *Response) failure() error {
	if r.failed != nil {
		return r.failed
	}
	if r.Result == nil || r.Result.Error == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", r.Result.Error.Type, r.Result.Error.Message)
}
