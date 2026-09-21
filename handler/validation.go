package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Validation failures are answered with structured detail, in the shape the
// other language SDKs answer with, so a client written against one Celerity
// API reads the same body from all of them:
//
//	{
//	  "message": "the request body is not valid",
//	  "details": [
//	    {"code": "invalid_type", "path": ["total"], "message": "expected a number"}
//	  ]
//	}
//
// What goes in a detail is what a caller can act on including which field, and what was
// wrong with it, in their terms. What stays out is anything describing how this
// is implemented, since a caller cannot act on a Go type name and should not be
// told one.

// ValidationIssue is one thing wrong with an input.
type ValidationIssue struct {
	// Code identifies the kind of problem, so a client can branch on it without
	// parsing prose. It is a stable, lowercase identifier.
	Code string `json:"code"`
	// Path names the field, outermost first, and is empty when the problem is
	// with the input as a whole.
	Path []string `json:"path"`
	// Message says what is wrong in terms of the caller's own request.
	Message string `json:"message"`
}

// Codes carried by issues this SDK raises itself. A validator is free to use
// its own, and anything it reports is passed through.
const (
	// CodeInvalid is the fallback where nothing more specific is known.
	CodeInvalid = "invalid"
	// CodeInvalidType is a value of the wrong type, such as a string where a
	// number was expected.
	CodeInvalidType = "invalid_type"
	// CodeMalformed is an input that could not be read at all, such as a body
	// that is not valid JSON.
	CodeMalformed = "malformed"
)

// IssueReporter is implemented by an error that can describe itself as
// validation issues.
//
// It is the seam a validation library is adapted through, and the reason core
// doesn't depend on one. A library's own error type is wrapped in something that
// implements this, and its issues are answered verbatim. It mirrors the one
// method Schema interface the Node SDK validates through.
type IssueReporter interface {
	error
	ValidationIssues() []ValidationIssue
}

// ValidationError is an input that failed validation, answered as a 400 with
// its issues as details.
type ValidationError struct {
	// Message summarises the failure. It should name what was being validated,
	// such as the body or the query, rather than repeating the issues.
	Message string
	Issues  []ValidationIssue
	// Err is the underlying cause, reachable through errors.Is and
	// errors.Unwrap. Never answered to the caller, and so deliberately absent
	// from Error, as a validation library's own error text commonly names types
	// and internal paths. Code that wants it in a log unwraps it.
	Err error
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString(e.Message)
	for _, issue := range e.Issues {
		b.WriteString("; ")
		if len(issue.Path) > 0 {
			b.WriteString(strings.Join(issue.Path, "."))
			b.WriteString(": ")
		}
		b.WriteString(issue.Message)
	}
	return b.String()
}

// StatusCode satisfies [StatusCoder]: a failed validation is the caller's
// mistake.
func (e *ValidationError) StatusCode() int { return http.StatusBadRequest }

// ValidationIssues satisfies [IssueReporter].
func (e *ValidationError) ValidationIssues() []ValidationIssue { return e.Issues }

// Details satisfies [DetailCarrier], putting the issues in the answer.
func (e *ValidationError) Details() any { return e.Issues }

func (e *ValidationError) Unwrap() error { return e.Err }

// ErrorType classifies this failure for the protocol's error type field,
// satisfying [TypedError].
func (e *ValidationError) ErrorType() string { return "ValidationError" }

// Invalid returns a validation error carrying the given issues.
func Invalid(message string, issues ...ValidationIssue) *ValidationError {
	if message == "" {
		message = "the request is not valid"
	}
	return &ValidationError{Message: message, Issues: issues}
}

// Issue is shorthand for one issue about one field.
func Issue(code, message string, path ...string) ValidationIssue {
	return ValidationIssue{Code: code, Path: path, Message: message}
}

// IssuesFrom converts an error into issues.
//
// An error that reports its own is answered with those. Anything else becomes a
// single issue naming nothing, since a message this SDK did not compose may
// describe the implementation and is not repeated to the caller.
func IssuesFrom(err error) []ValidationIssue {
	if reporter, ok := errors.AsType[IssueReporter](err); ok {
		return reporter.ValidationIssues()
	}

	return []ValidationIssue{
		{
			Code:    CodeInvalid,
			Path:    []string{},
			Message: "the value is not valid",
		},
	}
}

// IssuesFromDecode turns a JSON decoding failure into issues a caller can act
// on.
//
// The standard library's messages name Go types and struct fields, as in
// "cannot unmarshal string into Go struct field order.total of type float64".
// That tells a caller nothing they can use and describes the implementation, so
// the field is kept and the rest is replaced.
func IssuesFromDecode(err error) []ValidationIssue {
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return []ValidationIssue{{
			Code:    CodeInvalidType,
			Path:    fieldPath(typeErr.Field),
			Message: "expected " + describeJSONType(typeErr.Type.String()),
		}}
	}

	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return []ValidationIssue{{
			Code:    CodeMalformed,
			Path:    []string{},
			Message: "the body is not valid JSON",
		}}
	}

	return []ValidationIssue{{
		Code:    CodeMalformed,
		Path:    []string{},
		Message: "the body could not be read",
	}}
}

func fieldPath(field string) []string {
	if field == "" {
		return []string{}
	}

	return strings.Split(field, ".")
}

// Names a Go type in terms of what JSON a caller should have
// sent, so the answer stays in the caller's vocabulary.
func describeJSONType(goType string) string {
	switch {
	case strings.HasPrefix(goType, "[]"):
		return "an array"
	case strings.HasPrefix(goType, "map["), strings.HasPrefix(goType, "struct"):
		return "an object"
	}

	switch goType {
	case "string":
		return "a string"
	case "bool":
		return "a boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "json.Number":
		return "a number"
	default:
		// Anything else is a type of this application's own, which a caller has
		// no way to reason about and is not told.
		return "a different value"
	}
}
