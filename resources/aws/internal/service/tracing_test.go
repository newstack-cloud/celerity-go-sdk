package service_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// A span per AWS call, under the span for the operation a handler asked for.
// Written here rather than taken from a library, so what it records is this
// package's to be sure of.
type TracingTestSuite struct {
	suite.Suite
}

func TestTracingTestSuite(t *testing.T) {
	suite.Run(t, new(TracingTestSuite))
}

// call runs one request through the middleware the session installs, as the
// SDK would, and answers the spans it produced.
func (s *TracingTestSuite) call(
	serviceID, operation string, status int, requestID string, failure error,
) []celeritytest.RecordedSpan {
	recorder := celeritytest.NewTracer(s.T())

	session := service.NewSessionWith(func(context.Context) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	})
	cfg, err := session.Config(awstest.Ctx())
	s.Require().NoError(err)

	stack := middleware.NewStack("test", smithyhttp.NewStackRequest)
	for _, option := range cfg.APIOptions {
		s.Require().NoError(option(stack))
	}

	ctx := awsmiddleware.SetServiceID(
		awsmiddleware.SetOperationName(awstest.Ctx(), operation), serviceID)

	handler := middleware.DecorateHandler(
		middleware.HandlerFunc(func(
			ctx context.Context, in any,
		) (any, middleware.Metadata, error) {
			var metadata middleware.Metadata
			if requestID != "" {
				awsmiddleware.SetRequestIDMetadata(&metadata, requestID)
			}
			return &smithyhttp.Response{
				Response: &http.Response{StatusCode: status},
			}, metadata, failure
		}), stack)

	_, _, err = handler.Handle(ctx, nil)
	if failure != nil {
		s.Require().Error(err)
	}
	return recorder.Spans()
}

func (s *TracingTestSuite) Test_a_call_is_traced_as_the_service_and_the_operation() {
	// OpenTelemetry's own convention for a remote call, so an AWS span from
	// here sits alongside one from anywhere else in a trace.
	spans := s.call("S3", "GetObject", http.StatusOK, "req-1", nil)

	s.Require().Len(spans, 1)
	span := spans[0]
	s.Equal("S3.GetObject", span.Name)
	s.True(span.Ended)

	system, ok := span.Attr("rpc.system")
	s.Require().True(ok)
	s.Equal("aws-api", system)

	service, _ := span.Attr("rpc.service")
	s.Equal("S3", service)
	method, _ := span.Attr("rpc.method")
	s.Equal("GetObject", method)
}

func (s *TracingTestSuite) Test_a_call_records_the_request_id_and_the_status() {
	// The request id is what an AWS support case is opened with.
	spans := s.call("DynamoDB", "Query", http.StatusOK, "req-abc", nil)

	s.Require().Len(spans, 1)
	id, ok := spans[0].Attr("aws.request_id")
	s.Require().True(ok)
	s.Equal("req-abc", id)

	code, ok := spans[0].Attr("http.response.status_code")
	s.Require().True(ok)
	s.EqualValues(200, code)
}

func (s *TracingTestSuite) Test_a_call_that_failed_records_what_failed() {
	refused := errors.New("AccessDenied")

	spans := s.call("SQS", "SendMessage", http.StatusForbidden, "req-2", refused)

	s.Require().Len(spans, 1)
	s.ErrorIs(spans[0].Err, refused)
	code, _ := spans[0].Attr("http.response.status_code")
	s.EqualValues(403, code)
}

func (s *TracingTestSuite) Test_a_call_with_no_service_named_is_still_traced() {
	// Rather than producing a span called ".", which would be worse than one
	// saying it does not know.
	spans := s.call("", "", http.StatusOK, "", nil)

	s.Require().Len(spans, 1)
	s.Equal("AWS.Unknown", spans[0].Name)
}

func (s *TracingTestSuite) Test_a_call_is_traced_under_whatever_span_it_was_made_inside() {
	// Which is what puts an AWS call under the operation a handler asked for,
	// and that under the dispatch.
	recorder := celeritytest.NewTracer(s.T())

	session := service.NewSessionWith(func(context.Context) (aws.Config, error) {
		return aws.Config{Region: "eu-west-1"}, nil
	})
	cfg, err := session.Config(awstest.Ctx())
	s.Require().NoError(err)

	stack := middleware.NewStack("test", smithyhttp.NewStackRequest)
	for _, option := range cfg.APIOptions {
		s.Require().NoError(option(stack))
	}

	outer, span := telemetry.CurrentTracer().Start(awstest.Ctx(), "celerity.bucket.get")
	ctx := awsmiddleware.SetServiceID(
		awsmiddleware.SetOperationName(outer, "GetObject"), "S3")

	handler := middleware.DecorateHandler(
		middleware.HandlerFunc(func(context.Context, any) (any, middleware.Metadata, error) {
			return &smithyhttp.Response{Response: &http.Response{StatusCode: 200}},
				middleware.Metadata{}, nil
		}), stack)
	_, _, err = handler.Handle(ctx, nil)
	s.Require().NoError(err)
	span.End()

	call, found := recorder.Span("S3.GetObject")
	s.Require().True(found)
	s.Equal("celerity.bucket.get", call.Parent)
}
