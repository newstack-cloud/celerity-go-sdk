package celerity_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/guard"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Validation answers with structured detail a caller can act on, in the shape
// the other language SDKs answer with, and without anything describing how
// this is implemented.
type ValidateTestSuite struct {
	suite.Suite
}

func TestValidateTestSuite(t *testing.T) {
	suite.Run(t, new(ValidateTestSuite))
}

type createOrder struct {
	CustomerID string  `json:"customerId"`
	Total      float64 `json:"total"`
}

// Rejects itself, which is the zero-configuration seam.
type selfChecking struct {
	Total float64 `json:"total"`
}

func (r selfChecking) Validate() error {
	if r.Total <= 0 {
		return celerity.Invalid("the order is not valid",
			celerity.Issue("too_small", "must be greater than zero", "total"))
	}
	return nil
}

// The body shape every Celerity SDK answers a refusal with.
type answer struct {
	Message string                    `json:"message"`
	Details []handler.ValidationIssue `json:"details"`
}

func (s *ValidateTestSuite) answerFrom(res *handler.Result) answer {
	s.Require().NotNil(res.HTTP)

	var got answer
	s.Require().NoError(json.Unmarshal(res.HTTP.Body, &got), "body: %s", res.HTTP.Body)
	return got
}

func (s *ValidateTestSuite) Test_a_self_validating_input_answers_with_its_issues() {
	res, err := runHandler(s.T(), func(_ context.Context, in selfChecking) (string, error) {
		s.Fail("the handler ran despite the input being invalid")
		return "", nil
	}, &handler.Request{Method: "POST", Body: []byte(`{"total":0}`)})

	s.Require().NoError(err, "a rejected input is an answer, not a fault")
	s.Equal(400, res.HTTP.Status)

	got := s.answerFrom(res)
	s.Equal("the order is not valid", got.Message)
	s.Require().Len(got.Details, 1)
	s.Equal(handler.ValidationIssue{
		Code:    "too_small",
		Path:    []string{"total"},
		Message: "must be greater than zero",
	}, got.Details[0])
}

func (s *ValidateTestSuite) Test_a_valid_input_reaches_the_handler() {
	res, err := runHandler(s.T(), func(_ context.Context, in selfChecking) (selfChecking, error) {
		return in, nil
	}, &handler.Request{Method: "POST", Body: []byte(`{"total":12.5}`)})

	s.Require().NoError(err)
	s.Equal(200, res.HTTP.Status)
}

// A body of the wrong shape is the case where a leak is easiest.
// The decoder's own message is "cannot unmarshal string into Go struct field
// createOrder.total of type float64".
func (s *ValidateTestSuite) Test_a_type_error_names_the_field_and_not_the_go_type() {
	res, err := runHandler(s.T(), func(_ context.Context, in createOrder) (string, error) {
		return "", nil
	}, &handler.Request{Method: "POST", Body: []byte(`{"total":"free"}`)})

	s.Require().NoError(err)
	s.Equal(400, res.HTTP.Status)

	got := s.answerFrom(res)
	s.Require().Len(got.Details, 1)
	s.Equal("invalid_type", got.Details[0].Code)
	s.Equal([]string{"total"}, got.Details[0].Path, "the caller is told which field")
	s.Equal("expected a number", got.Details[0].Message)

	// What must not appear: the Go type, the struct, the package.
	body := string(res.HTTP.Body)
	for _, leak := range []string{"float64", "Go struct", "createOrder", "unmarshal"} {
		s.NotContains(body, leak, "the answer describes the implementation")
	}
}

func (s *ValidateTestSuite) Test_a_malformed_body_says_so_without_an_offset() {
	res, err := runHandler(s.T(), func(_ context.Context, in createOrder) (string, error) {
		return "", nil
	}, &handler.Request{Method: "POST", Body: []byte(`{"total":`)})

	s.Require().NoError(err)
	s.Equal(400, res.HTTP.Status)

	got := s.answerFrom(res)
	s.Require().Len(got.Details, 1)
	s.Equal("malformed", got.Details[0].Code)
	s.Equal("the body is not valid JSON", got.Details[0].Message)
}

// stubValidator stands in for a struct-tag validation library.
type stubValidator struct{ issues []handler.ValidationIssue }

func (v stubValidator) Validate(any) error {
	if len(v.issues) == 0 {
		return nil
	}
	return celerity.Invalid("the request is not valid", v.issues...)
}

func (s *ValidateTestSuite) Test_an_application_wide_validator_is_run() {
	app := celerity.New(celerity.WithValidator(stubValidator{issues: []handler.ValidationIssue{
		{Code: "required", Path: []string{"customerId"}, Message: "is required"},
	}}))
	celerity.Post(app, "/orders", func(_ context.Context, in createOrder) (string, error) {
		s.Fail("the handler ran despite the validator rejecting the input")
		return "", nil
	}, celerity.Named("createOrder"))
	s.Require().NoError(app.Err())

	reg, ok := app.Registry().Get(celerity.HTTPTag("POST", "/orders"))
	s.Require().True(ok)

	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{"total":1}`)},
	})

	s.Require().NoError(err)
	s.Equal(400, res.HTTP.Status)

	got := s.answerFrom(res)
	s.Require().Len(got.Details, 1)
	s.Equal("required", got.Details[0].Code)
	s.Equal([]string{"customerId"}, got.Details[0].Path)
}

// A validator that reports nothing structured is the common case for a library
// this SDK has no adapter for.
func (s *ValidateTestSuite) Test_an_unstructured_failure_is_not_repeated_to_the_caller() {
	leaky := errors.New("Key: 'createOrder.Total' Error:Field validation for 'Total' failed on the 'gt' tag")

	app := celerity.New(celerity.WithValidator(failingValidator{err: leaky}))
	celerity.Post(app, "/orders", func(_ context.Context, in createOrder) (string, error) {
		return "", nil
	}, celerity.Named("createOrder"))
	s.Require().NoError(app.Err())

	reg, _ := app.Registry().Get(celerity.HTTPTag("POST", "/orders"))
	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{"total":1}`)},
	})
	s.Require().NoError(err)

	body := string(res.HTTP.Body)
	s.NotContains(body, "createOrder", "the validator's own text names Go identifiers")
	s.NotContains(body, "'gt' tag")

	// The caller gets a generic issue rather than nothing, so the refusal is
	// still actionable as a refusal.
	got := s.answerFrom(res)
	s.Require().Len(got.Details, 1)
	s.Equal(handler.CodeInvalid, got.Details[0].Code)
}

// The cause is kept for logs even though it never reaches the caller, so an
// operator can still see which rule the input broke.
func (s *ValidateTestSuite) Test_an_unstructured_cause_is_kept_for_logs() {
	cause := errors.New("Field validation for 'Total' failed on the 'gt' tag")
	wrapped := &handler.ValidationError{
		Message: "the request is not valid",
		Issues:  handler.IssuesFrom(cause),
		Err:     cause,
	}

	s.ErrorIs(wrapped, cause)
	s.True(strings.Contains(wrapped.Error(), "gt"), "Error() should carry the cause")
}

type failingValidator struct{ err error }

func (v failingValidator) Validate(any) error { return v.err }

// Validation is not a layer, because a layer runs over the untyped event and
// validation runs over the decoded input. What a layer would have given, per
// handler control, is a registration option instead.

func (s *ValidateTestSuite) Test_a_handler_can_opt_out_of_validation() {
	app := celerity.New(celerity.WithValidator(stubValidator{issues: []handler.ValidationIssue{
		{Code: "required", Path: []string{"customerId"}, Message: "is required"},
	}}))

	reached := false
	celerity.Post(app, "/passthrough", func(_ context.Context, in createOrder) (string, error) {
		reached = true
		return "ok", nil
	}, celerity.Named("passthrough"), celerity.SkipValidation())
	s.Require().NoError(app.Err())

	reg, ok := app.Registry().Get(celerity.HTTPTag("POST", "/passthrough"))
	s.Require().True(ok)

	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{}`)},
	})

	s.Require().NoError(err)
	s.True(reached, "the handler should have run despite the application validator")
	s.Equal(200, res.HTTP.Status)
}

func (s *ValidateTestSuite) Test_opting_out_also_skips_the_inputs_own_check() {
	// Skipping means skipping, rather than leaving one of the two seams running
	// and the other not.
	app := celerity.New()

	reached := false
	celerity.Post(app, "/passthrough", func(_ context.Context, in selfChecking) (string, error) {
		reached = true
		return "ok", nil
	}, celerity.Named("passthrough"), celerity.SkipValidation())
	s.Require().NoError(app.Err())

	reg, _ := app.Registry().Get(celerity.HTTPTag("POST", "/passthrough"))
	_, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{"total":0}`)},
	})

	s.Require().NoError(err)
	s.True(reached, "the input's own Validate should have been skipped too")
}

func (s *ValidateTestSuite) Test_one_handler_can_use_different_rules() {
	app := celerity.New(celerity.WithValidator(stubValidator{}))

	stricter := stubValidator{
		issues: []handler.ValidationIssue{
			{
				Code:    "forbidden_here",
				Path:    []string{"total"},
				Message: "this endpoint will not take that",
			},
		},
	}

	celerity.Post(app, "/strict", func(_ context.Context, in createOrder) (string, error) {
		s.Fail("the handler ran despite its own validator rejecting the input")
		return "", nil
	}, celerity.Named("strict"), celerity.ValidateWith(stricter))
	s.Require().NoError(app.Err())

	reg, _ := app.Registry().Get(celerity.HTTPTag("POST", "/strict"))
	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{"total":1}`)},
	})

	s.Require().NoError(err)
	s.Equal(400, res.HTTP.Status)

	got := s.answerFrom(res)
	s.Require().Len(got.Details, 1)
	s.Equal("forbidden_here", got.Details[0].Code)
}

// Guards run outside the handler wrapper, so an unauthenticated caller is
// refused before anything describes their input back to them.
func (s *ValidateTestSuite) Test_a_guard_refuses_before_validation_reports_anything() {
	app := celerity.New(celerity.WithValidator(stubValidator{issues: []handler.ValidationIssue{
		{Code: "required", Path: []string{"customerId"}, Message: "is required"},
	}}))
	celerity.AddGuard(app, "deny", func(context.Context, *guard.Request) (guard.Decision, error) {
		return guard.Deny("not for you"), nil
	})
	celerity.Post(app, "/guarded", func(_ context.Context, in createOrder) (string, error) {
		return "", nil
	}, celerity.Named("guarded"), celerity.ProtectedBy("deny"))
	s.Require().NoError(app.Err())

	reg, _ := app.Registry().Get(celerity.HTTPTag("POST", "/guarded"))
	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{}`)},
	})

	s.Require().NoError(err)
	s.Equal(401, res.HTTP.Status, "the guard should answer first")
	s.NotContains(string(res.HTTP.Body), "customerId",
		"an unauthenticated caller should not be told about the shape of the input")
}
