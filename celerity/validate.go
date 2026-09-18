package celerity

import (
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Validation is library-agnostic.
// Core does not depend on a specific validation library,
// and whichever one you choose for your application can be adapted to it.
//
// There are two ways in, and both run:
//
//  1. The decoded input validates itself, by implementing [SelfValidating].
//     Nothing to configure, and the rules live with the type they constrain.
//  2. An application-wide [Validator], for struct-tag libraries where the rules
//     are on the fields and something has to walk them.

// SelfValidating is implemented by a request type that checks itself.
//
// Returning a [handler.ValidationError] answers with structured detail;
// returning anything else is answered as a 400 with no detail, since a message
// this SDK did not compose may describe the implementation.
//
//	func (r CreateOrder) Validate() error {
//	    if r.Total <= 0 {
//	        return celerity.Invalid("the order is not valid",
//	            celerity.Issue("too_small", "must be greater than zero", "total"))
//	    }
//	    return nil
//	}
type SelfValidating interface {
	Validate() error
}

// Validator checks a decoded input.
//
// It is what a struct-tag validation library is adapted through. The adapter
// should return a [handler.ValidationError], or any error implementing
// [handler.IssueReporter], so the caller is answered with issues rather than
// with a bare message.
type Validator interface {
	Validate(input any) error
}

// WithValidator sets the validator run over every decoded input, after the
// input's own Validate where it has one.
func WithValidator(v Validator) Option {
	return func(o *options) { o.validator = v }
}

// Returns the check to run over a handler's decoded input, and
// nil where the handler opted out.
//
// Resolved at registration rather than per event as what validates a handler
// cannot change once it is registered, so deciding it once is both cheaper and
// one less thing that can differ between two dispatches.
func (a *App) validationFor(o registerOptions) func(any) error {
	if o.skipValidation {
		return nil
	}

	v := o.validator
	if v == nil {
		v = a.options.validator
	}

	return func(in any) error { return validateInput(v, in) }
}

// Runs both seams and returns the first failure.
//
// The input's own check runs first as it is the more specific of the two, and a
// type that rejects itself should say so in its own terms rather than being
// described by a generic walk over its fields.
func validateInput(v Validator, in any) error {
	if self, ok := in.(SelfValidating); ok {
		if err := self.Validate(); err != nil {
			return asValidationError(err)
		}
	}

	if v != nil {
		if err := v.Validate(in); err != nil {
			return asValidationError(err)
		}
	}

	return nil
}

// Makes sure a failure answers with issues rather than with
// whatever text the validator produced.
func asValidationError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*handler.ValidationError); ok {
		return err
	}
	if _, ok := err.(handler.IssueReporter); ok {
		return &handler.ValidationError{
			Message: "the request is not valid",
			Issues:  handler.IssuesFrom(err),
			Err:     err,
		}
	}

	// Nothing structured to report, and the error's own text is not repeated:
	// a validation library commonly names Go types and struct fields, which a
	// caller cannot act on and should not be shown.
	return &handler.ValidationError{
		Message: "the request is not valid",
		Issues:  handler.IssuesFrom(err),
		Err:     err,
	}
}

// Re-exported so an application builds issues without a second import.
var (
	Invalid = handler.Invalid
	Issue   = handler.Issue
)

type (
	ValidationError = handler.ValidationError
	ValidationIssue = handler.ValidationIssue
)
