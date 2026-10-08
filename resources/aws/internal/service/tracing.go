package service

import (
	"context"
	"net/http"
	"strconv"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// A span per AWS call, under the span for the operation a handler asked for.
//
// The resources package already traces what the application asked for:
// celerity.bucket.get names an object storage read. This names what AWS was
// actually asked, once per attempt, which is where a read that was slow because
// it was retried three times stops looking like one slow read.
//
// Written here rather than taken from a library. OpenTelemetry's own AWS
// instrumentation was deprecated in October 2026 for want of a maintainer, and
// the compile-time successor is a build wrapper rather than a package and
// routes through the deprecated one anyway. What is needed is a span naming the
// service, the operation and the outcome, which is a middleware against an
// extension point the AWS SDK documents and will not withdraw.

// tracingMiddleware is added to the deserialize step, which is the innermost
// place that sees one attempt: a retried call passes through it once per
// attempt, and a span per attempt is the point.
type tracingMiddleware struct{}

func (tracingMiddleware) ID() string {
	return "CelerityTracing"
}

func (tracingMiddleware) HandleDeserialize(
	ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
) (middleware.DeserializeOutput, middleware.Metadata, error) {
	service := awsmiddleware.GetServiceID(ctx)
	operation := awsmiddleware.GetOperationName(ctx)

	var metadata middleware.Metadata
	out, err := telemetry.Traced(ctx, spanName(service, operation), callAttrs(service, operation),
		func(ctx context.Context, span telemetry.Span) (middleware.DeserializeOutput, error) {
			out, md, err := next.HandleDeserialize(ctx, in)
			metadata = md
			span.SetAttributes(outcomeAttrs(md, out)...)
			return out, err
		})
	return out, metadata, err
}

// spanName is the service and operation, which is how OpenTelemetry's own
// conventions name a remote call and what a backend groups one by.
func spanName(service, operation string) string {
	if service == "" {
		return "AWS.Unknown"
	}
	if operation == "" {
		return service
	}
	return service + "." + operation
}

// callAttrs are OpenTelemetry's conventions for a remote procedure call, used
// rather than names of Celerity's own so that an AWS span from here sits
// alongside one from anywhere else in a trace.
func callAttrs(service, operation string) []telemetry.Attr {
	return []telemetry.Attr{
		telemetry.String("rpc.system", "aws-api"),
		telemetry.String("rpc.service", service),
		telemetry.String("rpc.method", operation),
	}
}

// outcomeAttrs are what is only known once AWS has answered.
//
// The request id is what an AWS support case is opened with, and the attempt is
// what separates one slow call from four quick ones.
func outcomeAttrs(
	metadata middleware.Metadata, out middleware.DeserializeOutput,
) []telemetry.Attr {
	attrs := make([]telemetry.Attr, 0, 3)
	if id, ok := awsmiddleware.GetRequestIDMetadata(metadata); ok {
		attrs = append(attrs, telemetry.String("aws.request_id", id))
	}
	if response, ok := out.RawResponse.(*smithyhttp.Response); ok && response != nil {
		attrs = append(attrs,
			telemetry.Int("http.response.status_code", response.StatusCode),
			telemetry.String("http.response.status", statusText(response.StatusCode)),
		)
	}
	return attrs
}

func statusText(code int) string {
	if text := http.StatusText(code); text != "" {
		return text
	}
	return strconv.Itoa(code)
}

// traceCalls adds the middleware to every client built from a configuration.
func traceCalls(stack *middleware.Stack) error {
	return stack.Deserialize.Add(tracingMiddleware{}, middleware.Before)
}
