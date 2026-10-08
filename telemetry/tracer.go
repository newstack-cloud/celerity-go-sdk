package telemetry

import (
	"context"
	"sync/atomic"
)

// Tracer starts spans.
//
// Shaped after OpenTelemetry's own tracer, since that is
// what a Go application already traces with and what the implementation behind
// this seam adapts to. It is declared here so that core doesn't depend on a tracing
// library, an application that traces nothing links none, and one that does
// links the module holding it.
//
// A span is started with the context it belongs to and returns a new one
// carrying it, which is what makes the next span a child of this one. Most
// callers will use [Traced] rather than this.
type Tracer interface {
	Start(ctx context.Context, name string, attrs ...Attr) (context.Context, Span)
}

// Span is one timed operation.
type Span interface {
	// SetAttributes adds to what the span records, for something not known
	// when it started.
	SetAttributes(attrs ...Attr)
	// RecordError marks the span as failed and records what failed.
	RecordError(err error)
	// End stops the span. Every span has to be ended, and ending one twice is
	// the implementation's to tolerate rather than the caller's to avoid.
	End()
	// Context identifies the span within the trace it belongs to, so that a log
	// written during it can be found from the trace and the trace from the log.
	Context() SpanContext
}

// SpanContext identifies one span within one trace.
//
// Strings rather than the byte arrays a tracing library holds them as, because
// what they are for here is being written into a log record and read back by a
// person or a query. The zero value is what a span that is not being recorded
// answers with, which is also what an application exporting nothing gets.
type SpanContext struct {
	TraceID string
	SpanID  string
}

// Recorded reports whether the span belongs to a trace that is being recorded.
func (c SpanContext) Recorded() bool {
	return c.TraceID != ""
}

// Log attributes naming the trace a record belongs to.
//
// The names OpenTelemetry's own conventions use, so that a backend correlating
// logs with traces finds them without being told where to look.
const (
	TraceIDKey = "trace_id"
	SpanIDKey  = "span_id"
)

// Attr is one key and value recorded on a span.
//
// The value is typed by the constructors rather than by the field, so that an
// implementation translating to its own representation has something to switch
// on and a caller cannot record a value no backend accepts.
type Attr struct {
	Key   string
	Value any
}

// String records a string attribute.
func String(key, value string) Attr {
	return Attr{Key: key, Value: value}
}

// Int records a whole-number attribute.
func Int(key string, value int) Attr {
	return Attr{Key: key, Value: int64(value)}
}

// Int64 records a whole-number attribute.
func Int64(key string, value int64) Attr {
	return Attr{Key: key, Value: value}
}

// Float64 records a number attribute that is not whole.
func Float64(key string, value float64) Attr {
	return Attr{Key: key, Value: value}
}

// Bool records a true-or-false attribute.
func Bool(key string, value bool) Attr {
	return Attr{Key: key, Value: value}
}

// The tracer this process traces with.
//
// Process-wide rather than carried on the application, for the same reason a
// resource provider is. Which implementation is linked is a build's decision
// and the same for every dispatch, so threading it through every resource
// handle would be plumbing that never varies. Read through an atomic pointer
// because a handler may trace while something else is still installing one.
var installed atomic.Pointer[Tracer]

// SetTracer installs the tracer this process traces with, replacing any
// already installed.
//
// Called from the init of the module holding an implementation, which is what
// lets the generated platform file be a blank import rather than wiring. An
// application supplying its own, or a test supplying a recorder, calls it
// directly.
func SetTracer(t Tracer) {
	if t == nil {
		installed.Store(nil)
		return
	}

	installed.Store(&t)
}

// CurrentTracer returns the installed tracer, and a tracer that records nothing
// where none is installed.
//
// Never nil, so a caller traces unconditionally and an application that linked
// no implementation pays a call that does nothing rather than a branch at every
// call site.
func CurrentTracer() Tracer {
	if t := installed.Load(); t != nil {
		return *t
	}

	return noopTracer{}
}

// Traced runs fn inside a span, recording a failure and ending the span
// whatever happens.
//
// What a resource operation uses, so that tracing one is a line rather than a
// span's whole lifecycle:
//
//	return telemetry.Traced(ctx, "celerity.bucket.get",
//	    []telemetry.Attr{telemetry.String("bucket.key", key)},
//	    func(ctx context.Context, _ telemetry.Span) (*bucket.Object, error) { ... })
//
// The context fn is given is the span's, so anything it traces in turn is a
// child. The span is given as well, for what is only known once the call has
// been made, such as how many entries of a batch were taken, or how large an answer
// was. A panic ends the span and records nothing of its own before carrying on
// up, since the recovery that can describe it is the dispatch loop's.
func Traced[T any](
	ctx context.Context,
	name string,
	attrs []Attr,
	fn func(context.Context, Span) (T, error),
) (T, error) {
	ctx, span := CurrentTracer().Start(ctx, name, attrs...)
	defer span.End()

	result, err := fn(ctx, span)
	if err != nil {
		span.RecordError(err)
	}
	return result, err
}

// TracedCall is [Traced] for an operation that answers with nothing but a
// failure.
func TracedCall(
	ctx context.Context,
	name string,
	attrs []Attr,
	fn func(context.Context, Span) error,
) error {
	_, err := Traced(ctx, name, attrs, func(ctx context.Context, span Span) (struct{}, error) {
		return struct{}{}, fn(ctx, span)
	})
	return err
}

// noopTracer is what an application that linked no implementation traces with.
type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, _ string, _ ...Attr) (context.Context, Span) {
	return ctx, noopSpan{}
}

type noopSpan struct{}

func (noopSpan) SetAttributes(...Attr) {}
func (noopSpan) RecordError(error)     {}
func (noopSpan) End()                  {}
func (noopSpan) Context() SpanContext {
	return SpanContext{}
}
