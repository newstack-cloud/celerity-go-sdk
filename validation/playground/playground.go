// Package playground adapts github.com/go-playground/validator to Celerity's
// validation seam.
//
//	app := celerity.New(celerity.WithValidator(playground.New()))
//
//	type CreateOrder struct {
//	    CustomerID string  `json:"customerId" validate:"required"`
//	    Total      float64 `json:"total"      validate:"gt=0"`
//	}
//
// It lives in its own package rather than in core so that an application which
// doesn't require validation does not link it. Importing it is the only thing that does.
package playground

import (
	"errors"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Validator checks struct tags and reports what failed as Celerity issues.
type Validator struct {
	validate *validator.Validate
	// Describes a rule to the caller, by tag. A custom rule without
	// one is still reported, naming the rule, but a rule worth writing is
	// usually worth explaining.
	messages map[string]func(validator.FieldError) string
	// pending holds registrations until New has a *validator.Validate to apply
	// them to, since options run before it is built.
	pending []func(*validator.Validate)
}

// Option configures the validator.
type Option func(*Validator)

// WithValidate supplies a preconfigured *validator.Validate, for an application
// that registers rules, aliases or struct-level checks of its own.
//
// The field-name function is still applied over it, so paths stay in the
// caller's vocabulary. Give its rules messages with [WithMessage].
func WithValidate(v *validator.Validate) Option {
	return func(a *Validator) { a.validate = v }
}

// WithRule registers a custom rule and what to tell a caller who breaks it.
//
//	playground.New(
//	    playground.WithRule("sku", validSKU, "must be a SKU, such as AB-1234"),
//	)
//
//	type CreateOrder struct {
//	    SKU string `json:"sku" validate:"required,sku"`
//	}
//
// The rule and its message are registered together because a rule without one
// is reported as "failed the sku rule", which says less than it could to the
// one person who needs it.
func WithRule(tag string, fn validator.Func, message string) Option {
	return func(a *Validator) {
		a.pending = append(a.pending, func(v *validator.Validate) {
			_ = v.RegisterValidation(tag, fn)
		})
		a.withMessage(tag, func(validator.FieldError) string { return message })
	}
}

// WithMessage sets what a caller is told when a rule rejects their input.
//
// Use it for rules registered elsewhere: the library's own rules, an alias, or
// a struct-level check registered through [WithValidate].
func WithMessage(tag, message string) Option {
	return func(a *Validator) {
		a.withMessage(tag, func(validator.FieldError) string { return message })
	}
}

// WithMessageFunc sets a message composed from the failure, for a rule whose
// explanation depends on its parameter or on the value.
//
//	playground.WithMessageFunc("divisibleby", func(fe validator.FieldError) string {
//	    return "must be divisible by " + fe.Param()
//	})
func WithMessageFunc(tag string, message func(validator.FieldError) string) Option {
	return func(a *Validator) { a.withMessage(tag, message) }
}

func (a *Validator) withMessage(tag string, message func(validator.FieldError) string) {
	if a.messages == nil {
		a.messages = map[string]func(validator.FieldError) string{}
	}
	a.messages[tag] = message
}

// New returns a validator over struct tags.
func New(opts ...Option) *Validator {
	a := &Validator{}
	for _, opt := range opts {
		opt(a)
	}
	if a.validate == nil {
		a.validate = validator.New(validator.WithRequiredStructEnabled())
	}
	for _, register := range a.pending {
		register(a.validate)
	}

	// Paths are reported in the names the caller sent, not the Go field names.
	// Without this a caller who sent "customerId" is told about "CustomerID",
	// which is a field they have never seen and cannot correct.
	a.validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		switch name {
		case "-":
			return ""
		case "":
			return field.Name
		default:
			return name
		}
	})

	return a
}

// Validate satisfies celerity.Validator.
func (a *Validator) Validate(input any) error {
	if input == nil {
		return nil
	}

	// Only structs carry the tags this reads. Anything else is not something
	// this validator has an opinion about, and reporting it as invalid would
	// refuse requests nothing is wrong with.
	if kind := reflect.Indirect(reflect.ValueOf(input)).Kind(); kind != reflect.Struct {
		return nil
	}

	err := a.validate.Struct(input)
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*validator.InvalidValidationError](err); ok {
		// The value could not be validated at all, which is a fault in the
		// application rather than the caller's mistake.
		return err
	}

	var fieldErrors validator.ValidationErrors
	if !errors.As(err, &fieldErrors) {
		return err
	}

	return &handler.ValidationError{
		Message: "the request is not valid",
		Issues:  a.issuesFrom(fieldErrors),
		Err:     err,
	}
}

func (a *Validator) issuesFrom(fieldErrors validator.ValidationErrors) []handler.ValidationIssue {
	issues := make([]handler.ValidationIssue, 0, len(fieldErrors))
	for _, fe := range fieldErrors {
		issues = append(issues, handler.ValidationIssue{
			Code:    fe.Tag(),
			Path:    pathOf(fe),
			Message: a.messageFor(fe),
		})
	}
	return issues
}

// Prefers a message the application configured, so a custom rule
// explains itself and a built-in one can be reworded without forking this.
func (a *Validator) messageFor(fe validator.FieldError) string {
	if message, ok := a.messages[fe.Tag()]; ok {
		return message(fe)
	}
	return messageFor(fe)
}

// Returns the field's path in the names the caller sent.
//
// Namespace leads with the Go struct's own name, which is this application's
// business and not the caller's, so the first segment is dropped.
func pathOf(fe validator.FieldError) []string {
	namespace := fe.Namespace()
	if _, rest, found := strings.Cut(namespace, "."); found {
		namespace = rest
	}

	if namespace == "" {
		return []string{}
	}

	return strings.Split(namespace, ".")
}

// Describes the rule in terms of the value the caller sent.
//
// The library's own Error() is "Key: 'CreateOrder.Total' Error:Field validation
// for 'Total' failed on the 'gt' tag", which names Go identifiers and the tag
// syntax. A caller can act on neither.
func messageFor(fe validator.FieldError) string {
	param := fe.Param()

	switch fe.Tag() {
	case "required", "required_if", "required_unless", "required_with":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "url", "uri":
		return "must be a valid URL"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "gt":
		return "must be greater than " + param
	case "gte", "min":
		return minMessage(fe, param)
	case "lt":
		return "must be less than " + param
	case "lte", "max":
		return maxMessage(fe, param)
	case "len":
		return "must be exactly " + param + " characters long"
	case "eq":
		return "must be " + param
	case "ne":
		return "must not be " + param
	case "oneof":
		return "must be one of: " + strings.Join(strings.Fields(param), ", ")
	case "alpha":
		return "must contain only letters"
	case "alphanum":
		return "must contain only letters and numbers"
	case "numeric":
		return "must be numeric"
	default:
		// An unknown rule still names the field and the rule that rejected it,
		// which is more useful than silence and carries nothing internal.
		if param != "" {
			return "failed the " + fe.Tag() + " rule (" + param + ")"
		}
		return "failed the " + fe.Tag() + " rule"
	}
}

// A length rule reads differently for a string than for a number, and the
// caller is told whichever applies to what they sent.
func minMessage(fe validator.FieldError, param string) string {
	if isMeasuredByLength(fe) {
		return "must be at least " + param + " characters long"
	}
	return "must be at least " + param
}

func maxMessage(fe validator.FieldError, param string) string {
	if isMeasuredByLength(fe) {
		return "must be at most " + param + " characters long"
	}
	return "must be at most " + param
}

func isMeasuredByLength(fe validator.FieldError) bool {
	switch fe.Kind() {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return true
	default:
		return false
	}
}
