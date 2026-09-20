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
//
// # What the platform decides rather than the SDK
//
// A handler is written once and should see the same event whichever side
// mapped it. Where API Gateway has already made a decision the Celerity
// runtime does not, that decision stands and is documented on the mapping
// rather than undone:
//
//   - Query parameters are read from the raw query string rather than from the
//     platform's own joined map, so repetition survives exactly as the client
//     sent it, which is what the runtime gives a handler.
//   - Repeated headers arrive combined into one comma-separated value, and
//     payload format 2.0 has no multi-value header field, so the division the
//     client made is not recoverable. Headers whose grammar makes every comma a
//     separator are divided back up, along with the few that clients actually
//     repeat; the rest are carried whole, because splitting them would invent a
//     division that may never have been made. HTTP-date headers are never
//     divided: the comma after the day name is part of the date. See
//     SplitHeaders and SplitHeadersEnvVar.
//   - Cookies are never divided, the platform keeping them out of the headers
//     for the same reason, so they are folded into a cookie header on the way
//     in and carried as Cookies on the way out.
//   - A catch-all path parameter arrives as one value with its separators
//     still in it. It is split into segments, because that is the one case
//     where not translating would change what a handler binding it receives.
//   - A WebSocket connection carries text frames only, and a client sending a
//     binary frame is disconnected, so the sender here implements
//     handler.WebSocketSender and deliberately not handler.BinarySender.
//   - API Gateway reports whether it accepted a pushed message and nothing
//     about what the client made of it, so the acknowledgement fields on
//     handler.OutboundMessage have nothing to wait for and are ignored.
//
// # Configuration
//
// What this package reads from the environment is read once, when the adapter
// is built at process start, rather than per invocation: the environment does
// not change under a running function, and a warm function should not be able
// to change its behaviour beneath itself. The exception is Detect, which is
// asked once during adapter selection and decides whether this adapter runs at
// all.
//
// # Acknowledging a client's message
//
// The WebSocket Runtime Protocol has a message that asked to be acknowledged
// acknowledged on receipt. The Celerity runtime does that itself; API Gateway
// does not, so the adapter does, before the handler runs and whether or not
// the message reaches a handler at all. An application implements none of it.
//
// Because the transport carries no binary frames, the protocol's capabilities
// signal cannot reach a client here, and its absence is what tells the client
// it is in a constrained environment where acknowledgements are JSON text.
//
// # Bucket and datastore consumers
//
// Every Celerity consumer arrives over SQS, including one bound to a bucket or
// a datastore: the notification is delivered to a queue and the message body
// carries the originating event. Those bodies are unwrapped, and S3 and
// DynamoDB event names are mapped onto Celerity's own vocabulary, so a handler
// bound to a bucket is given the object event rather than an SQS message that
// happens to contain one.
//
// Reporting a failed message requires the function to be configured with
// ReportBatchItemFailures. Without it, SQS does not read the partial-failure
// response and deletes a batch where one message failed.
package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// FunctionNameEnvVar is what every Lambda execution environment sets, and is
// how this adapter knows it is the one running.
const FunctionNameEnvVar = "AWS_LAMBDA_FUNCTION_NAME"

// Name identifies this adapter in the handler manifest, in logs and in errors.
const Name = "aws-lambda"

func init() { serverless.Register(New()) }

// Mapper translates between AWS event shapes and the SDK's vocabulary.
//
// A mapper only translates, every method is a function of its payload
// and the configuration the mapper was built with.
//
// That configuration is captured once, by [NewMapper], rather than read from
// the environment per request. The environment does not change under a running
// function, so reading it on each invocation would be work to reach the same
// answer, and it would leave the behaviour of a warm function dependent on
// something that could in principle be changed beneath it.
type Mapper struct {
	splitHeaders map[string]bool
	handlerKind  handler.Kind
	handlerID    string
}

// NewMapper returns a mapper configured from the environment.
func NewMapper() Mapper {
	kind, _ := kindFromEnv()
	return Mapper{
		splitHeaders: buildSplitNames(os.Getenv(SplitHeadersEnvVar)),
		handlerKind:  kind,
		handlerID:    os.Getenv(serverless.HandlerIDEnvVar),
	}
}

// Adapter runs Celerity handlers on AWS Lambda.
//
// An application does not construct one. Importing this package registers it,
// and it is selected at startup when [FunctionNameEnvVar] says the process is
// running on Lambda.
type Adapter struct {
	mapper  Mapper
	senders sync.Map // endpoint -> *sender
}

// New returns the adapter, with its configuration captured from the
// environment.
//
// Called from this package's init, so the environment is read once at process
// start rather than on an invocation. An application does not call it; a test
// that wants an adapter configured from what it has just set does.
func New() *Adapter {
	return &Adapter{mapper: NewMapper()}
}

// Name identifies the adapter.
func (a *Adapter) Name() string {
	return Name
}

// Detect reports whether this process is running on Lambda.
//
// Read live rather than captured, unlike the rest of the environment this
// package reads as it is asked once, while an adapter is being selected at
// startup, and it is the question that decides whether this adapter is used at
// all rather than configuration for one that already is.
func (a *Adapter) Detect() bool {
	return os.Getenv(FunctionNameEnvVar) != ""
}

// Mapper returns the event mapper.
func (a *Adapter) Mapper() serverless.EventMapper {
	return a.mapper
}

// Start hands control to the Lambda runtime and does not return until the
// execution environment is shutting down.
//
// The payload reaches the invoker as the bytes Lambda delivered. Lambda
// serialises whatever the invoker returns, which for each source is already the
// shape that source reads whether to be an API Gateway response, an SQS partial-failure
// report, or the handler's own output.
func (a *Adapter) Start(ctx context.Context, invoke serverless.Invoker) error {
	lambda.StartWithOptions(
		func(ctx context.Context, payload json.RawMessage) (any, error) {
			return invoke(ctx, payload)
		},
		lambda.WithContext(ctx),
	)
	// StartWithOptions does not return, it exits the process when the runtime
	// API closes. Reached only if that ever changes.
	return nil
}

// WebSocketSender builds a sender for the connection an event came from,
// implementing [serverless.WebSocketSenderProvider].
//
// One sender per endpoint, kept for the life of the execution environment, so
// the AWS client and its credentials are built once rather than per invocation.
// Keyed by endpoint rather than held singly because the endpoint comes from the
// event, and a function can serve more than one stage or domain.
func (a *Adapter) WebSocketSender(
	_ context.Context,
	payload []byte,
) (handler.WebSocketSender, error) {
	endpoint, err := ManagementEndpoint(payload)
	if err != nil {
		return nil, err
	}
	if endpoint == "https:///" {
		return nil, fmt.Errorf("the websocket event carries no domain or stage to push back to")
	}

	if existing, ok := a.senders.Load(endpoint); ok {
		return existing.(*sender), nil
	}
	created, _ := a.senders.LoadOrStore(endpoint, newSender(endpoint))
	return created.(*sender), nil
}

// AcknowledgeReceipt tells a client its message arrived, implementing
// [serverless.ReceiptAcknowledger].
//
// The protocol has a message that asked to be acknowledged on
// receipt, which is what lets a client stop its resend timer without waiting on
// however long the handler takes. API Gateway does not do it, so the SDK does,
// an application does not implement any of this, and a handler acknowledging its own
// messages would be reimplementing the protocol once per application.
func (a *Adapter) AcknowledgeReceipt(ctx context.Context, msg *handler.WebSocketMessage) error {
	messageID, asked := clientAckRequest(string(msg.Message))
	if !asked {
		return nil
	}

	sender, ok := serverless.WebSocketSenderFrom(ctx)
	if !ok {
		return fmt.Errorf("no websocket sender to acknowledge message %s on", messageID)
	}

	ack, err := composeClientAck(messageID, time.Now().Unix())
	if err != nil {
		return err
	}
	return sender.Send(ctx, handler.OutboundMessage{
		ConnectionID: msg.ConnectionID,
		Message:      ack,
	})
}
