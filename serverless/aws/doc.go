// Package aws adapts Celerity handlers to AWS Lambda.
//
// It is selected by importing it, which is how an application picks a platform
// without naming one anywhere else:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
//
// An init registers the adapter, and Detect answers from
// AWS_LAMBDA_FUNCTION_NAME, so that variable is known here and nowhere else in
// the SDK.
//
// It implements serverless.Adapter: an event mapper between AWS event shapes
// and the SDK's vocabulary, and a Start that hands control to lambda.Start.
// Everything with a rule in it, resolving which handler this function serves,
// caching it across warm invocations, running the layer pipeline, and deciding
// what an unroutable event means, lives in the serverless package and is
// inherited rather than reimplemented here.
//
// Mapping:
//
//	HTTP       API Gateway HTTP API v2 proxy  -> APIGatewayV2HTTPResponse
//	WebSocket  API Gateway WebSocket v2       -> {statusCode}
//	Consumer   SQS                            -> SQSEventResponse.BatchItemFailures
//	Schedule   EventBridge rule               -> nil
//	Custom     direct invoke payload          -> the handler's own output
package aws
