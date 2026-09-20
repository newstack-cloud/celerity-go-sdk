// Package serverless is the seam between Celerity handlers and a provider's
// serverless platform.
//
// In a containerised deployment the Celerity runtime routes the event and hands
// the handler something already in the SDK's vocabulary. In a serverless
// deployment the platform does that job in its own shapes instead, and an
// adapter translates.
//
// A provider implements two things, an [EventMapper] and [Adapter.Start].
// Everything with a rule in it, resolving which handler this function is,
// caching it across warm invocations, running the layer pipeline, and deciding
// what an unroutable event means, lives here and is inherited rather than
// reimplemented per provider.
package serverless

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Invoker handles one raw provider payload.
//
// The payload is the provider's own encoded event, and the returned value is
// whatever that provider's runtime expects to serialise back.
type Invoker func(ctx context.Context, payload []byte) (any, error)

// EventMapper converts between a provider's event shapes and the SDK's.
//
// A mapper only translates. It resolves nothing and decides nothing, so an
// unroutable event or a failing handler is never its concern.
type EventMapper interface {
	// Detect reports which source a payload came from, so a function that is
	// not told what it serves can still route.
	Detect(payload []byte) (handler.Kind, error)

	ToRequest(payload []byte) (*handler.Request, error)
	FromResponse(res *handler.Response) (any, error)

	ToWebSocketMessage(payload []byte) (*handler.WebSocketMessage, error)

	ToConsumerBatch(payload []byte, tag string) (*handler.ConsumerBatch, error)
	FromBatchResult(res *handler.BatchResult) (any, error)

	ToScheduleTrigger(payload []byte, tag string) (*handler.ScheduleTrigger, error)

	ToCustomInvoke(payload []byte) (*handler.CustomInvoke, error)
	FromCustomResult(res *handler.CustomInvokeResult) (any, error)
}

// Adapter is a provider's mapper together with its process entry point.
type Adapter interface {
	// Name identifies the adapter in logs and errors, such as "aws-lambda".
	Name() string
	// Detect reports whether this process is running on the adapter's platform,
	// which every serverless runtime makes knowable from the environment.
	//
	// It is what makes selecting an adapter an import rather than a decision the
	// application has to encode, and it is why one binary can carry adapters for
	// several platforms.
	Detect() bool
	Mapper() EventMapper
	// Start hands control to the provider's own event loop, which calls invoke
	// once per invocation and does not return until the process is shutting
	// down.
	Start(ctx context.Context, invoke Invoker) error
}

// WebSocketSenderProvider is implemented by adapters that can push to a
// WebSocket connection, such as through the API Gateway Management API.
//
// It is a separate interface rather than a method on [Adapter] so that an
// adapter for a platform without WebSocket support is not forced to stub it.
type WebSocketSenderProvider interface {
	// WebSocketSender builds a sender for the connection the payload came from,
	// since the endpoint to push to is commonly carried by the event itself.
	WebSocketSender(ctx context.Context, payload []byte) (handler.WebSocketSender, error)
}

// ReceiptAcknowledger is implemented by adapters whose transport leaves
// acknowledging a client's WebSocket message to the SDK.
//
// The WebSocket Runtime Protocol has a message that asked to be acknowledged
// acknowledged on receipt, which is what lets a client stop its resend timer
// without waiting on however long the work takes. The Celerity runtime does
// that itself, so this is empty there; a managed gateway does not, so the
// adapter does it and an application implements none of it.
//
// It is a separate interface rather than a method on [Adapter] for the reason
// [WebSocketSenderProvider] is: an adapter for a platform this does not apply
// to is not forced to stub it.
type ReceiptAcknowledger interface {
	// AcknowledgeReceipt tells the client its message arrived, where the message
	// asked to be told and the transport leaves that to the SDK.
	//
	// Called before the handler runs, and for a message that reaches no handler
	// at all: the client asked whether its message arrived, and it did.
	AcknowledgeReceipt(ctx context.Context, msg *handler.WebSocketMessage) error
}

// Handler is a resolved handler an adapter invokes, with its layers already
// applied.
type Handler struct {
	Tag  string
	Name string
	Kind handler.Kind
	// Invoke runs the full pipeline: application layers, then group, then
	// handler, with guards ahead of them.
	Invoke layer.Next
}

// Resolver finds the handler a payload should reach.
//
// It is an interface so that this package does not depend on the celerity
// package, which depends on it.
type Resolver interface {
	// ByName resolves a handler by blueprint resource name or published name,
	// which is what CELERITY_HANDLER_ID carries.
	ByName(name string) (*Handler, bool)
	// ByTag resolves a handler by its handler tag.
	ByTag(tag string) (*Handler, bool)
	// Only returns the single handler of a kind, when there is exactly one.
	// A Celerity deployment gives each handler its own function, so this is the
	// ordinary case and it resolves a function that was told nothing.
	Only(kind handler.Kind) (*Handler, bool)
}
