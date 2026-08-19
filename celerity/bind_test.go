package celerity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A catch-all path parameter arrives as one value per segment, which is the
// case a first-value-wins binder silently truncates.

type catchAll struct {
	Path     string   `path:"filePath"`
	Segments []string `path:"filePath"`
}

type scalars struct {
	OrderID string `path:"orderId"`
	Limit   int    `query:"limit"`
	Trace   string `header:"x-trace-id"`
}

func bindThrough[In any](t *testing.T, req *handler.Request) In {
	t.Helper()

	app := celerity.New()
	var bound In
	celerity.Get(app, "/bind", func(_ context.Context, in In) (string, error) {
		bound = in
		return "ok", nil
	})
	if err := app.Err(); err != nil {
		t.Fatalf("registering: %v", err)
	}

	reg, ok := app.Registry().Get(celerity.HTTPTag("GET", "/bind"))
	if !ok {
		t.Fatal("handler was not registered")
	}
	if _, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		Kind: handler.KindHTTP,
		HTTP: req,
	}); err != nil {
		t.Fatalf("running the pipeline: %v", err)
	}
	return bound
}

func TestCatchAllBinding(t *testing.T) {
	got := bindThrough[catchAll](t, &handler.Request{
		Method:     "GET",
		PathParams: handler.Params{"filePath": {"reports", "2026", "q1.pdf"}},
	})

	if got.Path != "reports/2026/q1.pdf" {
		t.Errorf("string binding = %q, want the segments rejoined", got.Path)
	}
	if len(got.Segments) != 3 || got.Segments[2] != "q1.pdf" {
		t.Errorf("slice binding = %v, want every segment", got.Segments)
	}
}

func TestCatchAllKeepsAnEncodedSeparatorInASegment(t *testing.T) {
	// The runtime splits segments before percent-decoding, so a segment holding
	// an encoded separator survives intact. Rejoining into a string cannot
	// preserve that, which is what the slice form is for.
	got := bindThrough[catchAll](t, &handler.Request{
		Method:     "GET",
		PathParams: handler.Params{"filePath": {"reports", "q1/final.pdf"}},
	})

	if len(got.Segments) != 2 || got.Segments[1] != "q1/final.pdf" {
		t.Errorf("slice binding = %v, want the encoded separator kept in one segment", got.Segments)
	}
}

func TestScalarBinding(t *testing.T) {
	got := bindThrough[scalars](t, &handler.Request{
		Method:      "GET",
		PathParams:  handler.Params{"orderId": {"order-1"}},
		QueryParams: handler.Params{"limit": {"25"}},
		Headers:     handler.Params{"x-trace-id": {"trace-1"}},
	})

	if got.OrderID != "order-1" || got.Limit != 25 || got.Trace != "trace-1" {
		t.Errorf("binding = %+v, want each source bound", got)
	}
}

func TestPathParameterWinsOverTheBody(t *testing.T) {
	// Binding runs after decoding, so a caller cannot spoof a path parameter
	// through the body.
	body, _ := json.Marshal(map[string]string{"orderId": "spoofed"})
	got := bindThrough[scalars](t, &handler.Request{
		Method:     "POST",
		Body:       body,
		PathParams: handler.Params{"orderId": {"genuine"}},
	})

	if got.OrderID != "genuine" {
		t.Errorf("OrderID = %q, want the path parameter to win", got.OrderID)
	}
}
