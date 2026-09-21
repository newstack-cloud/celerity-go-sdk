package handler_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A validation failure is answered to the caller, so what it says has to be the
// caller's vocabulary. The library that found the failure names Go types and
// internal paths in its own error text, and that must not reach anyone.
type ValidationTestSuite struct {
	suite.Suite
}

func TestValidationTestSuite(t *testing.T) {
	suite.Run(t, new(ValidationTestSuite))
}

func (s *ValidationTestSuite) Test_the_message_carries_the_issues_and_nothing_else() {
	err := &handler.ValidationError{
		Message: "the request is not valid",
		Issues: []handler.ValidationIssue{
			{Code: "required", Path: []string{"orderId"}, Message: "is required"},
			{Code: "lte", Path: []string{"discount"}, Message: "must be at most 100"},
		},
		// What a validation library hands back, naming the Go struct it was
		// given and the tag it failed on.
		Err: errors.New(
			"Key: 'pricingRequest.orderId' Error:Field validation for 'orderId' " +
				"failed on the 'required' tag",
		),
	}

	message := err.Error()

	s.Contains(message, "the request is not valid")
	s.Contains(message, "orderId: is required")
	s.Contains(message, "discount: must be at most 100")

	s.NotContains(message, "pricingRequest", "the Go type reached the caller")
	s.NotContains(message, "Field validation", "the library's own text reached the caller")
}

func (s *ValidationTestSuite) Test_the_cause_is_still_reachable_for_a_log() {
	cause := errors.New("the validator's own account")
	err := &handler.ValidationError{Message: "the request is not valid", Err: cause}

	s.Require().ErrorIs(err, cause, "unwrapping should still reach it")
	s.NotContains(err.Error(), "own account")
}

func (s *ValidationTestSuite) Test_a_nested_path_reads_as_the_caller_wrote_it() {
	err := &handler.ValidationError{
		Message: "the request is not valid",
		Issues: []handler.ValidationIssue{
			{Code: "required", Path: []string{"items", "0", "sku"}, Message: "is required"},
		},
	}

	s.Contains(err.Error(), "items.0.sku: is required")
}
