package celeritytest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// RecordedSpan is one span a handler produced.
type RecordedSpan struct {
	Name string
	// TraceID and SpanID are what a log written during the span carries, so a
	// test can assert that a record and a span name the same trace.
	TraceID string
	SpanID  string
	// Attrs are what the span recorded, in the order they were added.
	Attrs []telemetry.Attr
	// Err is what failed inside the span, and nil where nothing did.
	Err error
	// Parent is the name of the span this one opened inside, and empty for the
	// outermost. What a test usually wants to know about a resource span is
	// that it sits under the dispatch.
	Parent string
	// Ended reports whether the span was closed, which every span has to be.
	Ended bool
}

// Attr returns the value recorded under a key, and whether it was recorded.
func (s RecordedSpan) Attr(key string) (any, bool) {
	for _, attr := range s.Attrs {
		if attr.Key == key {
			return attr.Value, true
		}
	}
	return nil, false
}

// Tracer records the spans a handler produced rather than exporting them.
//
// What it is for is telemetry the application wrote: a span a handler opened
// with [telemetry.Traced], the attributes it set on it, the error it recorded.
// Those are the application's own behaviour, and a test asserting them is
// testing the handler.
//
//	tracer := celeritytest.NewTracer(t)
//	harness.POST(t, "/orders", celeritytest.JSONBody(order{ID: "o-1", Total: 10}))
//
//	span := tracer.AssertSpan(t, "orders.price_quote")
//	total, _ := span.Attr("order.total")
//
// Asserting the SDK's own span names is not what it is for. Those belong to the
// SDK and are the same in every language Celerity supports, so a test naming
// one asserts this package's behaviour rather than the application's, and fails
// when a name here changes without the application having changed at all.
//
// Only one recorder can be installed at a time, because the tracer seam is
// process-wide. See [NewTracer].
type Tracer struct {
	mu    sync.Mutex
	spans []*RecordedSpan
}

// NewTracer installs a recording tracer and removes it when the test ends.
//
// Fails the test where a recorder is already installed. The tracer is installed
// process-wide rather than carried per dispatch, so two recorders at once means
// the second collects the first's spans as well as its own and both tests read
// something other than what they produced. A test calling this therefore cannot
// call t.Parallel.
func NewTracer(t testing.TB) *Tracer {
	t.Helper()

	if _, already := telemetry.CurrentTracer().(*Tracer); already {
		t.Fatalf("celeritytest: a recording tracer is already installed. " +
			"The tracer seam is process-wide, so two recording at once collect " +
			"each other's spans, a test calling NewTracer cannot call t.Parallel, " +
			"and cannot be nested inside another that called NewTracer")
	}

	recorder := &Tracer{}
	telemetry.SetTracer(recorder)
	t.Cleanup(func() {
		telemetry.SetTracer(nil)
	})
	return recorder
}

// AssertSpan returns the first span recorded under a name, failing the test
// where nothing was recorded under it.
//
// What was recorded is reported on failure, since a span that is missing is
// usually one recorded under a different name rather than one never opened.
func (r *Tracer) AssertSpan(t testing.TB, name string) RecordedSpan {
	t.Helper()

	span, found := r.Span(name)
	if found {
		return span
	}

	recorded := "nothing"
	if names := r.Names(); len(names) > 0 {
		recorded = strings.Join(names, "\n\t")
	}
	t.Fatalf("celeritytest: no span was recorded as %q.\nRecorded:\n\t%s", name, recorded)
	return RecordedSpan{}
}

// Spans returns the spans recorded, in the order they were started.
func (r *Tracer) Spans() []RecordedSpan {
	r.mu.Lock()
	defer r.mu.Unlock()

	spans := make([]RecordedSpan, 0, len(r.spans))
	for _, span := range r.spans {
		spans = append(spans, *span)
	}
	return spans
}

// Names returns the names of the spans recorded, which is what most assertions
// are about.
func (r *Tracer) Names() []string {
	names := []string{}
	for _, span := range r.Spans() {
		names = append(names, span.Name)
	}
	return names
}

// Span returns the first span recorded under a name, and whether there was one.
func (r *Tracer) Span(name string) (RecordedSpan, bool) {
	for _, span := range r.Spans() {
		if span.Name == name {
			return span, true
		}
	}
	return RecordedSpan{}, false
}

// Reset forgets the spans recorded so far, for a test making more than one
// dispatch and asserting on each.
func (r *Tracer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = nil
}

// Start opens a span, recording it against the one it was opened inside.
func (r *Tracer) Start(
	ctx context.Context, name string, attrs ...telemetry.Attr,
) (context.Context, telemetry.Span) {
	parent, _ := ctx.Value(parentKey{}).(string)

	r.mu.Lock()
	// Counted rather than random, so a test asserting on a correlated log
	// record sees the same ids on every run.
	trace, _ := ctx.Value(traceKey{}).(string)
	if trace == "" {
		trace = fmt.Sprintf("trace-%d", len(r.spans)+1)
	}
	span := &RecordedSpan{
		Name:    name,
		Attrs:   attrs,
		Parent:  parent,
		TraceID: trace,
		SpanID:  fmt.Sprintf("span-%d", len(r.spans)+1),
	}
	r.spans = append(r.spans, span)
	r.mu.Unlock()

	ctx = context.WithValue(ctx, traceKey{}, trace)

	return context.WithValue(ctx, parentKey{}, name), &recordingSpan{recorder: r, span: span}
}

// parentKey carries the name of the span a context is inside, which is how a
// recorded span knows its parent without a real trace to read it from.
type parentKey struct{}

// traceKey carries the trace a context belongs to, so that spans opened inside
// one another share it the way a real trace does.
type traceKey struct{}

type recordingSpan struct {
	recorder *Tracer
	span     *RecordedSpan
}

func (s *recordingSpan) SetAttributes(attrs ...telemetry.Attr) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	s.span.Attrs = append(s.span.Attrs, attrs...)
}

func (s *recordingSpan) RecordError(err error) {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	s.span.Err = err
}

func (s *recordingSpan) Context() telemetry.SpanContext {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	return telemetry.SpanContext{
		TraceID: s.span.TraceID,
		SpanID:  s.span.SpanID,
	}
}

func (s *recordingSpan) End() {
	s.recorder.mu.Lock()
	defer s.recorder.mu.Unlock()
	s.span.Ended = true
}
