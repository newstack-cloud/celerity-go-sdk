package aws_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

type CustomTestSuite struct {
	suite.Suite
}

func TestCustomTestSuite(t *testing.T) {
	suite.Run(t, new(CustomTestSuite))
}

func (s *CustomTestSuite) Test_the_deployment_names_the_handler() {
	s.T().Setenv(serverless.HandlerIDEnvVar, "recalculatePricing")
	payload := []byte(`{"orderId":"ord_42"}`)

	invoke, err := awsadapter.NewMapper().ToCustomInvoke(payload)

	s.Require().NoError(err)
	s.Equal("recalculatePricing", invoke.HandlerName)
	s.Equal(payload, invoke.Input, "the caller's payload is carried through untouched")
}

func (s *CustomTestSuite) Test_an_envelope_addresses_a_handler_where_nothing_else_did() {
	invoke, err := awsadapter.NewMapper().ToCustomInvoke(
		[]byte(`{"handlerName":"recalculatePricing","payload":{"orderId":"ord_42"}}`))

	s.Require().NoError(err)
	s.Equal("recalculatePricing", invoke.HandlerName)
	s.JSONEq(`{"orderId":"ord_42"}`, string(invoke.Input))
}

func (s *CustomTestSuite) Test_the_deployment_wins_over_an_envelope() {
	// An application whose own input happens to carry a handlerName field is
	// only read that way when nothing else said which handler this is.
	s.T().Setenv(serverless.HandlerIDEnvVar, "recalculatePricing")
	payload := []byte(`{"handlerName":"somethingElse","payload":{"a":1}}`)

	invoke, err := awsadapter.NewMapper().ToCustomInvoke(payload)

	s.Require().NoError(err)
	s.Equal("recalculatePricing", invoke.HandlerName)
	s.Equal(payload, invoke.Input)
}

func (s *CustomTestSuite) Test_a_payload_that_is_not_an_envelope_is_the_input() {
	cases := []struct {
		name    string
		payload string
	}{
		{"an ordinary object", `{"orderId":"ord_42"}`},
		{"an array", `[1,2,3]`},
		{"not JSON at all", `plain text`},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			invoke, err := awsadapter.NewMapper().ToCustomInvoke([]byte(tc.payload))

			s.Require().NoError(err)
			s.Empty(invoke.HandlerName)
			s.Equal(tc.payload, string(invoke.Input))
		})
	}
}

func (s *CustomTestSuite) Test_json_output_is_returned_as_itself() {
	out, err := awsadapter.NewMapper().FromCustomResult(&handler.CustomInvokeResult{
		Output: []byte(`{"total":1999}`),
	})

	s.Require().NoError(err)
	// Not re-encoded, so the caller reads an object rather than a quoted
	// string containing one.
	s.Equal(json.RawMessage(`{"total":1999}`), out)
}

func (s *CustomTestSuite) Test_output_that_is_not_json_is_returned_as_text() {
	out, err := awsadapter.NewMapper().FromCustomResult(&handler.CustomInvokeResult{Output: []byte("done")})

	s.Require().NoError(err)
	s.Equal("done", out)
}

func (s *CustomTestSuite) Test_a_handler_that_answered_nothing() {
	cases := []struct {
		name string
		res  *handler.CustomInvokeResult
	}{
		{"an empty output", &handler.CustomInvokeResult{}},
		{"no result at all", nil},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			out, err := awsadapter.NewMapper().FromCustomResult(tc.res)

			s.Require().NoError(err)
			s.Nil(out)
		})
	}
}
