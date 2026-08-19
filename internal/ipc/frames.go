// Package ipc implements the handler side of the Celerity runtime's IPC
// protocol: one long-lived bidirectional stream carrying configuration, events,
// results, credit, WebSocket sends and shutdown.
//
// The frame loop is written against [Transport] rather than against gRPC types,
// so the protocol's behaviour can be exercised without a transport. The gRPC
// client is a thin adapter over it, which is the same separation the runtime
// draws on its own side.
package ipc

import (
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// ToRuntime is a frame travelling from the handler to the runtime. Exactly one
// field is set.
type ToRuntime struct {
	Ready    *Ready
	Result   *Result
	Credit   *CreditGrant
	WsSend   *WsSend
	Draining *Draining
}

// Result is a handler's outcome together with the credit it returns.
//
// The two travel as one frame because they must: a result that carries no grant
// shrinks the credit window permanently, so the protocol gives a result nowhere
// to omit it.
type Result struct {
	Outcome *handler.Result
	// CreditGrant is normally 1. Zero withholds, telling the runtime to stop
	// dispatching to this stream until a later [CreditGrant] frame.
	CreditGrant uint32
}

// FromRuntime is a frame travelling from the runtime to the handler. Exactly
// one field is set.
type FromRuntime struct {
	Config   *RuntimeConfig
	ReadyAck *ReadyAck
	Dispatch *handler.Event
	Cancel   *Cancel
	Drain    *Drain
	WsAck    *WsSendAck
}

// Ready declares what this handler process serves and how much work it will
// take. The runtime does not dispatch until it arrives.
type Ready struct {
	HandlerTags []string
	// InitialCredit is the total events the runtime may have in flight to this
	// process across all tags, which is the SDK's worker pool size.
	InitialCredit uint32
	SDKVersion    string
	// Limits cap individual tags so one slow tag cannot consume the whole
	// credit window and starve the others.
	Limits []HandlerLimit
}

// HandlerLimit caps how much of the credit window one tag may occupy.
type HandlerLimit struct {
	HandlerTag    string
	MaxConcurrent uint32
}

// ReadyAck reports whether the handler's tags line up with the blueprint.
type ReadyAck struct {
	Accepted bool
	// UnknownTags are registered by the handler but absent from the blueprint.
	UnknownTags []string
	// UnhandledTags are in the blueprint but not registered by the handler.
	UnhandledTags []string
}

// CreditGrant returns credit to the runtime.
//
// Credit is additive and is replenished only when the handler says so, never
// implicitly by a result arriving. Grants normally ride along with a result;
// this frame covers resizing the window and resuming after a withhold.
type CreditGrant struct {
	Additional uint32
}

// CancelReason says why an event was cancelled.
type CancelReason int

const (
	CancelUnspecified CancelReason = iota
	CancelDeadlineExceeded
	// CancelCallerGone means the originating caller went away, so nothing is
	// waiting for the result. Sent when an HTTP client disconnects, and
	// deliberately not when a WebSocket connection closes: a message is closer
	// to a queue message than to a request, so the work is still worth
	// finishing.
	CancelCallerGone
	CancelShutdown
)

// Cancel tells the handler to stop work nobody is waiting for.
//
// It may name an event that has already completed, which is ignored rather than
// treated as an error.
type Cancel struct {
	ID     string
	Reason CancelReason
}

// Drain says the runtime is going away: it stops dispatching and waits for
// in-flight work until the deadline.
type Drain struct {
	Deadline time.Time
}

// Draining says the handler is going away, letting a supervisor roll handler
// processes without dropping work.
type Draining struct {
	Deadline time.Time
}

// WsSend asks the runtime to deliver messages to WebSocket clients.
type WsSend struct {
	CorrelationID string
	Messages      []handler.OutboundMessage
}

// WsSendAck reports what happened to each message in a [WsSend].
type WsSendAck struct {
	CorrelationID string
	Success       bool
	Failures      []handler.SendFailure
}

// RuntimeConfig is what the runtime sends before the handler declares itself,
// so the SDK can check the handlers registered in code against the ones the
// blueprint declares.
type RuntimeConfig struct {
	TracingEnabled bool
	MetricsEnabled bool
	Handlers       []HandlerConfig
}

// HandlerConfig describes one handler the blueprint declares.
type HandlerConfig struct {
	HandlerName    string
	HandlerTag     string
	Timeout        time.Duration
	TracingEnabled bool
	// PublishedName is the name the blueprint publishes this handler under,
	// empty when it sets none.
	PublishedName string
}

// Tags returns the handler tags the blueprint declares.
func (c *RuntimeConfig) Tags() []string {
	tags := make([]string, 0, len(c.Handlers))
	for _, h := range c.Handlers {
		tags = append(tags, h.HandlerTag)
	}
	return tags
}

// Transport carries frames in both directions.
//
// The gRPC client stream is one implementation; an in-memory channel pair is
// another, and is what the protocol's behaviour is tested against.
type Transport interface {
	Send(*ToRuntime) error
	Recv() (*FromRuntime, error)
	Close() error
}
