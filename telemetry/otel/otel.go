// Package otel traces a Celerity application with OpenTelemetry.
//
// It is selected by importing it, which `celerity-go generate --telemetry otel`
// writes:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/telemetry/otel"
//
// An init installs the tracer, so the generated file is a blank import rather
// than wiring, and nothing else in an application changes, the spans a dispatch
// and a resource operation produce are core's, and this only decides where they
// go.
//
// # A module of its own
//
// go.opentelemetry.io is a large dependency, and on a serverless platform a
// cold start cost rather than only a disk one, so an application that exports
// no traces does not compile it in. Core declares the seam this implements and
// depends on no tracing library at all.
//
// # What a deployment configures
//
// Where CELERITY_TELEMETRY_ENABLED is true this builds a provider: an OTLP
// exporter to the collector the environment names, the service named in every
// span, W3C propagation and X-Ray's alongside it on AWS. [ReadSettings] is the
// whole of what it reads, and the defaults are the ones the other
// SDKs use, so one deployment configures every language alike. Where it is not
// true nothing is built and the application traces to OpenTelemetry's no-op.
//
// # Configuring it yourself
//
// An application that wants something else, a sampler of its own or an exporter
// this does not build, installs its provider with the OpenTelemetry SDK in the
// ordinary way and will override the default. The tracer resolves the provider per call rather
// than capturing one, and an init runs before main does. Pair it with
// [telemetry.SetFlusher] so that a shutdown hands over what it is holding.
package otel

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// scope names the instrumentation in the spans it produces, which is how a
// backend tells Celerity's spans from an application's own.
const scope = "github.com/newstack-cloud/celerity-go-sdk"

func init() {
	telemetry.SetTracer(New())

	// A failure here is logged to stderr, an application whose
	// collector is unreachable should still serve, tracing to the no-op, rather
	// than refuse to start because telemetry could not be set up.
	if _, err := Configure(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
}

// New returns a tracer starting spans on whatever tracer provider
// OpenTelemetry has been given.
//
// An application does not construct one, instead, importing this package installs it.
// Exported for a test, and for an application wiring its own order of
// initialisation.
func New() telemetry.Tracer {
	return tracer{}
}

type tracer struct{}

// Start resolves the tracer provider per call rather than once.
//
// OpenTelemetry's global provider is commonly set after init runs, by an
// application's own setup, and a tracer captured here at install time would
// keep starting spans on the no-op that preceded it.
func (tracer) Start(
	ctx context.Context, name string, attrs ...telemetry.Attr,
) (context.Context, telemetry.Span) {
	ctx, started := otel.Tracer(scope).Start(ctx, name,
		oteltrace.WithAttributes(convert(attrs)...))
	return ctx, span{inner: started}
}

type span struct {
	inner oteltrace.Span
}

func (s span) SetAttributes(attrs ...telemetry.Attr) {
	s.inner.SetAttributes(convert(attrs)...)
}

// RecordError records what failed and marks the span failed with it.
//
// Both, because the two say different things: the event carries the error and
// the status is what a backend filters failed spans by.
func (s span) RecordError(err error) {
	if err == nil {
		return
	}
	s.inner.RecordError(err)
	s.inner.SetStatus(codes.Error, err.Error())
}

func (s span) End() { s.inner.End() }

// Context is the span's place in its trace, as the strings a log record
// carries, and zero where nothing is recording it.
func (s span) Context() telemetry.SpanContext {
	sc := s.inner.SpanContext()
	if !sc.IsValid() {
		return telemetry.SpanContext{}
	}
	return telemetry.SpanContext{
		TraceID: sc.TraceID().String(),
		SpanID:  sc.SpanID().String(),
	}
}

// Turns the seam's attributes into OpenTelemetry's.
//
// A value of a type the seam's constructors cannot produce is recorded as its
// printed form rather than dropped, since a span missing an attribute is harder
// to explain than one carrying a string.
func convert(attrs []telemetry.Attr) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}

	converted := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		switch value := attr.Value.(type) {
		case string:
			converted = append(converted, attribute.String(attr.Key, value))
		case int64:
			converted = append(converted, attribute.Int64(attr.Key, value))
		case float64:
			converted = append(converted, attribute.Float64(attr.Key, value))
		case bool:
			converted = append(converted, attribute.Bool(attr.Key, value))
		default:
			converted = append(converted, attribute.Stringer(attr.Key, printed{value}))
		}
	}
	return converted
}

type printed struct {
	value any
}

func (p printed) String() string {
	return fmt.Sprint(p.value)
}
