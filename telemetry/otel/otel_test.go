package otel_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
	celerityotel "github.com/newstack-cloud/celerity-go-sdk/telemetry/otel"
)

// What the seam's spans become once OpenTelemetry has them. Asserted against
// OpenTelemetry's own recorder rather than a stand-in, since what is worth
// knowing is that a real span carries what the seam was given.
type OTelTestSuite struct {
	suite.Suite
	spans *tracetest.SpanRecorder
}

func TestOTelTestSuite(t *testing.T) {
	suite.Run(t, new(OTelTestSuite))
}

func (s *OTelTestSuite) SetupTest() {
	s.spans = tracetest.NewSpanRecorder()
	otel.SetTracerProvider(trace.NewTracerProvider(trace.WithSpanProcessor(s.spans)))
}

func (s *OTelTestSuite) ended() []trace.ReadOnlySpan {
	return s.spans.Ended()
}

func (s *OTelTestSuite) Test_a_span_reaches_opentelemetry_with_its_name_and_scope() {
	tracer := celerityotel.New()

	_, span := tracer.Start(s.T().Context(), "celerity.bucket.get")
	span.End()

	ended := s.ended()
	s.Require().Len(ended, 1)
	s.Equal("celerity.bucket.get", ended[0].Name())
	s.Equal("github.com/newstack-cloud/celerity-go-sdk", ended[0].InstrumentationScope().Name,
		"named so a backend tells the SDK's spans from an application's own")
}

func (s *OTelTestSuite) Test_every_attribute_type_the_seam_offers_survives_the_crossing() {
	tracer := celerityotel.New()

	_, span := tracer.Start(s.T().Context(), "op",
		telemetry.String("a.text", "value"),
		telemetry.Int("a.count", 7),
		telemetry.Int64("a.big", 9000),
		telemetry.Float64("a.ratio", 1.5),
		telemetry.Bool("a.flag", true),
	)
	span.End()

	ended := s.ended()
	s.Require().Len(ended, 1)
	s.Subset(ended[0].Attributes(), []attribute.KeyValue{
		attribute.String("a.text", "value"),
		attribute.Int64("a.count", 7),
		attribute.Int64("a.big", 9000),
		attribute.Float64("a.ratio", 1.5),
		attribute.Bool("a.flag", true),
	})
}

func (s *OTelTestSuite) Test_an_attribute_added_after_the_span_started_is_recorded() {
	// Which is what a batch's counts are, none of them are known before the
	// call the span covers.
	tracer := celerityotel.New()

	_, span := tracer.Start(s.T().Context(), "op")
	span.SetAttributes(telemetry.Int("queue.successful_count", 2))
	span.End()

	ended := s.ended()
	s.Require().Len(ended, 1)
	s.Contains(ended[0].Attributes(), attribute.Int64("queue.successful_count", 2))
}

func (s *OTelTestSuite) Test_a_failure_is_both_recorded_and_sets_the_status() {
	// The two say different things, the event carries the error and the status
	// is what a backend filters failed spans by.
	tracer := celerityotel.New()
	failed := errors.New("the queue is full")

	_, span := tracer.Start(s.T().Context(), "op")
	span.RecordError(failed)
	span.End()

	ended := s.ended()
	s.Require().Len(ended, 1)
	s.Equal(codes.Error, ended[0].Status().Code)
	s.Equal("the queue is full", ended[0].Status().Description)
	s.Require().Len(ended[0].Events(), 1, "the error is recorded as an event too")
	s.Equal("exception", ended[0].Events()[0].Name)
}

func (s *OTelTestSuite) Test_a_span_started_inside_another_is_its_child() {
	// Which is what makes a resource operation appear under the dispatch that
	// reached it, and is the whole reason Start returns a context.
	tracer := celerityotel.New()

	ctx, parent := tracer.Start(s.T().Context(), "celerity.handler.http")
	_, child := tracer.Start(ctx, "celerity.bucket.get")
	child.End()
	parent.End()

	ended := s.ended()
	s.Require().Len(ended, 2)
	s.Equal("celerity.bucket.get", ended[0].Name())
	s.Equal(ended[1].SpanContext().SpanID(), ended[0].Parent().SpanID())
	s.Equal(ended[1].SpanContext().TraceID(), ended[0].SpanContext().TraceID(),
		"and both belong to one trace")
}

func (s *OTelTestSuite) Test_the_provider_is_resolved_per_call_rather_than_once() {
	// An application commonly sets its tracer provider after init has run, and
	// a tracer captured at install time would keep starting spans on the no-op
	// that preceded it.
	tracer := celerityotel.New()

	replacedWith := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(trace.NewTracerProvider(trace.WithSpanProcessor(replacedWith)))

	_, span := tracer.Start(s.T().Context(), "op")
	span.End()

	s.Empty(s.ended(), "nothing went to the provider that was set first")
	s.Require().Len(replacedWith.Ended(), 1, "and the span went to the one set after")
}

func (s *OTelTestSuite) Test_importing_the_package_installs_the_tracer() {
	// The whole of the wiring: an init installs it, so the generated platform
	// file is a blank import rather than setup an application has to write.
	_, span := telemetry.CurrentTracer().Start(s.T().Context(), "celerity.handler.http")
	span.End()

	s.Require().Len(s.ended(), 1,
		"the seam's installed tracer is this package's, from its init")
}
