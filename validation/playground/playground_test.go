package playground_test

import (
	"regexp"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/validation/playground"
)

// What a caller is told when their input is rejected: the field they sent, the
// rule that rejected it, and nothing about how any of it is implemented.
type PlaygroundTestSuite struct {
	suite.Suite
}

func TestPlaygroundTestSuite(t *testing.T) {
	suite.Run(t, new(PlaygroundTestSuite))
}

type createOrder struct {
	CustomerID string  `json:"customerId" validate:"required"`
	Total      float64 `json:"total"      validate:"gt=0"`
	Email      string  `json:"email"      validate:"omitempty,email"`
	Tier       string  `json:"tier"       validate:"omitempty,oneof=standard priority"`
	Notes      string  `json:"notes"      validate:"omitempty,max=10"`
}

// issues runs the validator and returns what a caller would be told.
func (s *PlaygroundTestSuite) issues(v *playground.Validator, input any) []handler.ValidationIssue {
	err := v.Validate(input)
	s.Require().Error(err, "the input should have been rejected")

	var invalid *handler.ValidationError
	s.Require().ErrorAs(err, &invalid)
	return invalid.Issues
}

// byPath indexes issues by the field they name.
func byPath(issues []handler.ValidationIssue) map[string]handler.ValidationIssue {
	indexed := map[string]handler.ValidationIssue{}
	for _, issue := range issues {
		key := ""
		if len(issue.Path) > 0 {
			key = issue.Path[0]
		}
		indexed[key] = issue
	}
	return indexed
}

func (s *PlaygroundTestSuite) Test_a_valid_input_is_accepted() {
	s.NoError(playground.New().Validate(createOrder{CustomerID: "c1", Total: 1}))
}

func (s *PlaygroundTestSuite) Test_issues_name_the_field_the_caller_sent() {
	got := byPath(s.issues(playground.New(), createOrder{}))

	// Not "CustomerID": a caller who sent customerId cannot correct a field
	// they have never seen.
	s.Equal("required", got["customerId"].Code)
	s.Equal("is required", got["customerId"].Message)
	s.Equal([]string{"customerId"}, got["customerId"].Path)

	s.Equal("gt", got["total"].Code)
	s.Equal("must be greater than 0", got["total"].Message)
}

func (s *PlaygroundTestSuite) Test_messages_are_in_the_callers_vocabulary() {
	cases := []struct {
		name  string
		input createOrder
		field string
		want  string
	}{
		{
			name:  "email",
			input: createOrder{CustomerID: "c1", Total: 1, Email: "nope"},
			field: "email",
			want:  "must be a valid email address",
		},
		{
			name:  "oneof lists what is allowed",
			input: createOrder{CustomerID: "c1", Total: 1, Tier: "gold"},
			field: "tier",
			want:  "must be one of: standard, priority",
		},
		{
			// A length rule on a string reads as characters; on a number it
			// would not.
			name:  "max on a string is a length",
			input: createOrder{CustomerID: "c1", Total: 1, Notes: "far too long to pass"},
			field: "notes",
			want:  "must be at most 10 characters long",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			got := byPath(s.issues(playground.New(), tc.input))
			s.Equal(tc.want, got[tc.field].Message)
		})
	}
}

// The library's own text is
// "Key: 'createOrder.Total' Error:Field validation for 'Total' failed on the
// 'gt' tag", which names Go identifiers and the tag syntax.
func (s *PlaygroundTestSuite) Test_nothing_in_an_issue_describes_the_implementation() {
	for _, issue := range s.issues(playground.New(), createOrder{}) {
		for _, leak := range []string{"createOrder", "Field validation", "Key:", "CustomerID"} {
			s.NotContains(issue.Message, leak)
			s.NotContains(issue.Path, leak)
		}
	}
}

var skuPattern = regexp.MustCompile(`^[A-Z]{2}-\d{4}$`)

type withSKU struct {
	SKU string `json:"sku" validate:"required,sku"`
}

func (s *PlaygroundTestSuite) Test_a_custom_rule_explains_itself() {
	v := playground.New(playground.WithRule("sku", func(fl validator.FieldLevel) bool {
		return skuPattern.MatchString(fl.Field().String())
	}, "must be a SKU, such as AB-1234"))

	s.Require().NoError(v.Validate(withSKU{SKU: "AB-1234"}))

	got := byPath(s.issues(v, withSKU{SKU: "banana"}))
	s.Equal("sku", got["sku"].Code, "the code is the tag, so a client can branch on it")
	s.Equal("must be a SKU, such as AB-1234", got["sku"].Message)
}

func (s *PlaygroundTestSuite) Test_a_custom_rule_without_a_message_still_names_itself() {
	// Registered through a preconfigured Validate, with no message given.
	base := validator.New(validator.WithRequiredStructEnabled())
	s.Require().NoError(base.RegisterValidation("sku", func(fl validator.FieldLevel) bool {
		return skuPattern.MatchString(fl.Field().String())
	}))

	got := byPath(s.issues(playground.New(playground.WithValidate(base)), withSKU{SKU: "banana"}))

	// Less useful than a written message, but it names the rule rather than
	// going silent, and it still leaks nothing.
	s.Equal("failed the sku rule", got["sku"].Message)
}

func (s *PlaygroundTestSuite) Test_a_message_can_be_composed_from_the_failure() {
	type payment struct {
		Amount int `json:"amount" validate:"multiple_of=5"`
	}

	base := validator.New()
	s.Require().NoError(base.RegisterValidation("multiple_of", func(fl validator.FieldLevel) bool {
		return fl.Field().Int()%5 == 0
	}))

	v := playground.New(
		playground.WithValidate(base),
		playground.WithMessageFunc("multiple_of", func(fe validator.FieldError) string {
			return "must be a multiple of " + fe.Param()
		}),
	)

	got := byPath(s.issues(v, payment{Amount: 7}))
	s.Equal("must be a multiple of 5", got["amount"].Message)
}

func (s *PlaygroundTestSuite) Test_a_built_in_rules_message_can_be_reworded() {
	v := playground.New(playground.WithMessage("required", "we need this one"))

	got := byPath(s.issues(v, createOrder{Total: 1}))
	s.Equal("we need this one", got["customerId"].Message)
}

func (s *PlaygroundTestSuite) Test_a_non_struct_input_is_not_this_validators_business() {
	// Refusing these would reject requests nothing is wrong with, since the
	// tags this reads only exist on structs.
	s.NoError(playground.New().Validate("a string"))
	s.NoError(playground.New().Validate(42))
	s.NoError(playground.New().Validate(nil))
}
