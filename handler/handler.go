// Package handler holds the vocabulary every Celerity handler is written
// against: the events a handler receives and the results it returns.
//
// The types here are deliberately free of any transport. The same
// [Request] reaches a handler whether it arrived over the runtime's IPC stream
// or was mapped from an API Gateway event by a serverless adapter, which is
// what lets one handler serve both.
package handler

import (
	"context"
	"time"
)

// Kind identifies the source an event came from.
type Kind string

const (
	KindHTTP      Kind = "http"
	KindWebSocket Kind = "websocket"
	KindConsumer  Kind = "consumer"
	KindSchedule  Kind = "schedule"
	KindCustom    Kind = "custom"
)

// Values holds the ordered values for one parameter or header name.
//
// Order within a name is significant (RFC 9110 section 5.3); order across names
// is not. Headers and parameters are multi-valued because collapsing them is
// lossy: RFC 9110 forbids folding two Set-Cookie headers into one
// comma-separated value.
type Values []string

// First returns the first value, or the empty string when there are none.
func (v Values) First() string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// Params maps a parameter or header name to its ordered values.
type Params map[string]Values

// Get returns the first value registered under name.
func (p Params) Get(name string) string {
	return p[name].First()
}

// Add appends a value under name, preserving order.
func (p Params) Add(name, value string) {
	p[name] = append(p[name], value)
}

// Set replaces every value under name with a single one.
func (p Params) Set(name, value string) {
	p[name] = Values{value}
}

// Request is an HTTP request as a handler sees it.
type Request struct {
	// Method is uppercase and canonical.
	Method string
	// Path is raw and percent-encoded, as received.
	Path string
	// Route is the template this request matched in the router's own form, so a
	// blueprint's /files/{path+} arrives as /files/{*path}.
	Route string
	// PathParams holds one value per segment for a catch-all parameter.
	// Segments are split from the raw path before being percent-decoded, so a
	// segment containing an encoded separator survives intact.
	PathParams  Params
	QueryParams Params
	// Headers have lowercase names.
	Headers  Params
	SourceIP string
	// RequestID identifies this delivery.
	RequestID string
	// Body is opaque. The media type comes from the content-type header.
	Body []byte
}

// Response is what an HTTP handler answers with.
type Response struct {
	// Status must be between 100 and 999. A value outside that range is a fault
	// in the handler, and the runtime answers 500 rather than narrowing it.
	Status  int
	Headers Params
	Body    []byte
}

// WebSocketMessage is one message received from a connected client.
type WebSocketMessage struct {
	Route        string
	ConnectionID string
	SourceIP     string
	// RequestID identifies this delivery, as distinct from MessageID.
	RequestID string
	Message   []byte
	// IsBinary reports whether this arrived as a binary frame rather than a
	// text one. A byte body alone cannot express the distinction and the
	// WebSocket protocol treats the two frame types as distinct.
	IsBinary bool
	// MessageID is the id the message is known by: the one the client gave it,
	// or one the runtime generated. It is what an acknowledgement names and what
	// a lost message notification refers to.
	MessageID string
}

// ConsumerBatch is a batch of messages from a queue, stream or event source.
type ConsumerBatch struct {
	Records    []ConsumerRecord
	SourceID   string
	SourceType string
	// Vendor is provider-specific and opaque.
	Vendor []byte
}

// ConsumerRecord is one message within a [ConsumerBatch].
type ConsumerRecord struct {
	MessageID  string
	Body       []byte
	Source     string
	EventType  string
	Attributes []byte
	Vendor     []byte
}

// BatchResult reports which records in a batch failed.
//
// Failures are per record so that a batch where one record fails redrives only
// that record. The runtime leaves the named records on their source and
// acknowledges the rest, so this decides what is delivered again rather than
// only reporting what happened.
//
// Naming nothing while returning an error answers for the whole batch, and
// none of it is acknowledged.
type BatchResult struct {
	Failures []RecordFailure
}

// Failed reports whether any record in the batch failed.
func (r *BatchResult) Failed() bool { return len(r.Failures) > 0 }

// Fail records a failure for one message, to be retried or redriven.
//
// The id must be the [ConsumerRecord.MessageID] the record arrived with. A
// name matching no record in the batch settles nothing, so the runtime refuses
// the whole answer rather than acknowledging a batch it cannot apply.
func (r *BatchResult) Fail(messageID string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	r.Failures = append(r.Failures, RecordFailure{MessageID: messageID, Error: msg})
}

// RecordFailure names one record that could not be processed.
type RecordFailure struct {
	// MessageID is the id the record arrived with, see [BatchResult.Fail].
	MessageID string
	Error     string
}

// ScheduleTrigger is a scheduled rule firing.
type ScheduleTrigger struct {
	ScheduleID string
	MessageID  string
	// Schedule is the expression the rule was declared with.
	Schedule string
	Input    []byte
	Vendor   []byte
}

// CustomInvoke is a direct invocation of a named handler.
type CustomInvoke struct {
	HandlerName string
	Input       []byte
}

// CustomInvokeResult is the answer to a [CustomInvoke].
//
// Output is opaque and carried to the caller exactly as given. Whether it has
// to be text is that caller's constraint: the runtime's local invoke endpoint
// answers in JSON, so it refuses output that is not valid UTF-8.
type CustomInvokeResult struct {
	Output []byte
}

// Event is one dispatch, carrying exactly one source.
//
// Handlers registered through the typed API never see this; it is the shape the
// layer pipeline and the adapters work in, where the source is not known
// statically.
type Event struct {
	// ID identifies this dispatch and is what a cancellation names.
	ID   string
	Tag  string
	Kind Kind
	// Timestamp is when the runtime received the originating event.
	Timestamp time.Time
	// Deadline is when the runtime stops waiting. It is applied to the handler's
	// context, and the runtime enforces it independently.
	Deadline time.Time
	// TraceContext carries W3C traceparent and tracestate.
	TraceContext map[string]string

	HTTP      *Request
	WebSocket *WebSocketMessage
	Consumer  *ConsumerBatch
	Schedule  *ScheduleTrigger
	Custom    *CustomInvoke
}

// Result is the outcome of one dispatch, carrying exactly one outcome.
type Result struct {
	ID string

	HTTP      *Response
	WebSocket *Ack
	Consumer  *BatchResult
	Schedule  *Ack
	Custom    *CustomInvokeResult
	// Error is set when user code returned or panicked with an unhandled error.
	Error *Error
}

// Ack acknowledges an event that produces no value of its own.
type Ack struct {
	Success bool
	Error   string
}

// Error describes an unhandled error in user code.
type Error struct {
	Message string
	Type    string
	Stack   string
}

func (e *Error) Error() string { return e.Message }

// Func is the untyped handler shape. Registration through the typed API wraps a
// generic function into one of these.
type Func func(ctx context.Context, ev *Event) (*Result, error)
