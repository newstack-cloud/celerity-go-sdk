package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// A handler says what an error means to a caller by returning an error that
// carries a status. Without one, every failure is a 500, which is right for a
// fault and wrong for the ordinary answers an HTTP API has to give where, for example,
// a missing record is a 404, a malformed body a 400.
//
// This lives here rather than in the celerity package because both paths need
// the same mapping. The runtime's dispatcher and a serverless adapter must
// answer a given error identically, or the same handler would behave
// differently depending on where it was deployed.

// StatusCoder is implemented by an error that knows the status it should be
// answered with.
//
// It is an interface as well as a concrete type so that an application's own
// error types can carry a status without importing this package's
// constructors, which is how a domain error stays a domain error.
type StatusCoder interface {
	error
	// StatusCode returns the HTTP status this error should be answered with.
	StatusCode() int
}

// DetailCarrier is implemented by an error that has structured detail to answer
// with alongside its message.
//
// The detail is serialised into the answer as-is, so it must carry nothing a
// caller cannot act on and nothing describing how this is implemented.
type DetailCarrier interface {
	error
	Details() any
}

// StatusError is an error carrying the status and message to answer with.
type StatusError struct {
	Status int
	// Message is what the caller is told. It should say what is wrong without
	// saying anything the caller is not entitled to know.
	Message string
	// Detail is structured information about what is wrong, answered alongside
	// the message. Nil where there is nothing to add.
	//
	// It reaches the caller, so it carries what they can act on and nothing
	// about how this is implemented.
	Detail any
	// Err is the underlying cause, kept for logging and errors.Is/As. It is
	// never sent to the caller.
	Err error
}

// Details satisfies [DetailCarrier].
func (e *StatusError) Details() any { return e.Detail }

// WithDetail returns a copy carrying structured detail.
func (e *StatusError) WithDetail(detail any) *StatusError {
	copied := *e
	copied.Detail = detail
	return &copied
}

func (e *StatusError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Status, e.Message, e.Err)
	}
	return fmt.Sprintf("%d %s", e.Status, e.Message)
}

// StatusCode satisfies [StatusCoder].
func (e *StatusError) StatusCode() int { return e.Status }

// Unwrap exposes the cause, so errors.Is and errors.As see through to it.
func (e *StatusError) Unwrap() error { return e.Err }

// Status returns an error answered with the given status.
//
// An empty message falls back to the status text, so the caller is never told
// only a number.
func Status(status int, message string) *StatusError {
	if message == "" {
		message = http.StatusText(status)
	}
	return &StatusError{
		Status:  status,
		Message: message,
	}
}

// Statusf returns an error answered with the given status, formatting the
// message. The message reaches the caller, so it should carry nothing internal.
func Statusf(status int, format string, args ...any) *StatusError {
	return &StatusError{
		Status:  status,
		Message: fmt.Sprintf(format, args...),
	}
}

// Wrap returns an error answered with the given status, keeping cause for
// logging and for errors.Is and errors.As. The cause is not sent to the caller.
func Wrap(status int, message string, cause error) *StatusError {
	if message == "" {
		message = http.StatusText(status)
	}
	return &StatusError{
		Status:  status,
		Message: message,
		Err:     cause,
	}
}

// The statuses an API reaches for most often. Anything else is [Status].
func BadRequest(message string) *StatusError   { return Status(http.StatusBadRequest, message) }
func Unauthorized(message string) *StatusError { return Status(http.StatusUnauthorized, message) }
func Forbidden(message string) *StatusError    { return Status(http.StatusForbidden, message) }
func NotFound(message string) *StatusError     { return Status(http.StatusNotFound, message) }
func Conflict(message string) *StatusError     { return Status(http.StatusConflict, message) }
func Gone(message string) *StatusError         { return Status(http.StatusGone, message) }

func UnprocessableEntity(message string) *StatusError {
	return Status(http.StatusUnprocessableEntity, message)
}

func TooManyRequests(message string) *StatusError {
	return Status(http.StatusTooManyRequests, message)
}

func NotImplemented(message string) *StatusError {
	return Status(http.StatusNotImplemented, message)
}

func ServiceUnavailable(message string) *StatusError {
	return Status(http.StatusServiceUnavailable, message)
}

// ResponseForError turns an error carrying a status into the response to answer
// with, and reports whether it carried one.
//
// An error that carries no status returns false rather than a 500. The caller
// is then free to report it as an unhandled failure, which is what keeps a
// fault visible to the runtime's error reporting instead of being flattened
// into an ordinary answer.
func ResponseForError(err error) (*Response, bool) {
	var coder StatusCoder
	if !errors.As(err, &coder) {
		return nil, false
	}

	status := coder.StatusCode()
	// A status outside what HTTP can express is a fault in the error rather
	// than an answer, and is left to be reported as one.
	if status < 100 || status > 999 {
		return nil, false
	}

	message := coder.Error()
	if statusErr, ok := errors.AsType[*StatusError](err); ok {
		// The cause is deliberately dropped as StatusError.Error includes it for
		// logs, and the caller is told only what the message says.
		message = statusErr.Message
	}

	if invalid, ok := errors.AsType[*ValidationError](err); ok {
		message = invalid.Message
	}

	var detail any
	if carrier, ok := errors.AsType[DetailCarrier](err); ok {
		detail = carrier.Details()
	}

	return JSONAnswer(status, message, detail), true
}

// JSONMessage builds the response body this SDK answers with wherever it
// answers on a handler's behalf, so a refusal from a guard and an error from a
// handler read the same way to a caller.
func JSONMessage(status int, message string) *Response {
	return JSONAnswer(status, message, nil)
}

// JSONAnswer builds the same body with structured detail alongside the message.
//
// The shape is the one the other language SDKs answer with, so a client
// written against a Celerity API in any language reads the same body:
//
//	{"message": "...", "details": [...]}
//
// Detail that cannot be encoded is dropped rather than failing the answer: a
// caller is better served by the status and the message than by a 500 raised
// while explaining a 400.
func JSONAnswer(status int, message string, detail any) *Response {
	answer := struct {
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	}{Message: message, Details: detail}

	body, err := json.Marshal(answer)
	if err != nil {
		body = fmt.Appendf(nil, `{"message":%q}`, message)
	}

	return &Response{
		Status:  status,
		Headers: Params{"content-type": {"application/json"}},
		Body:    body,
	}
}
