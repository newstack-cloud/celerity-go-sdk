package celerity_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A handler says what an error means to a caller by returning one that carries
// a status. Anything else is a fault, it stays an error so the runtime reports
// it and answers 500 itself, rather than being flattened into a reply that
// looks ordinary.
type ErrorsTestSuite struct {
	suite.Suite
}

func TestErrorsTestSuite(t *testing.T) {
	suite.Run(t, new(ErrorsTestSuite))
}

type getOrder struct {
	OrderID string `path:"orderId"`
}

// This puts a handler through the real pipeline and returns what the
// dispatcher would send back. Package-level and generic for the same reason
// bindThrough is, the handler's own types are the point.
func runHandler[In, Out any](
	t require.TestingT,
	h celerity.HandlerFunc[In, Out],
	req *handler.Request,
) (*handler.Result, error) {
	app := celerity.New()
	celerity.Post(app, "/things", h, celerity.Named("thing"))
	require.NoError(t, app.Err(), "registering")

	reg, ok := app.Registry().Get(celerity.HTTPTag("POST", "/things"))
	require.True(t, ok, "handler was not registered")

	return app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP, HTTP: req,
	})
}

func (s *ErrorsTestSuite) Test_a_status_carrying_error_becomes_that_status() {
	cases := []struct {
		name        string
		err         error
		wantStatus  int
		wantMessage string
	}{
		{"not found", celerity.NotFound("no such order"), 404, "no such order"},
		{"bad request", celerity.BadRequest("total must be positive"), 400, "total must be positive"},
		{"conflict", celerity.Conflict("already exists"), 409, "already exists"},
		{"arbitrary status", celerity.Status(418, "no coffee"), 418, "no coffee"},
		{
			// An empty message would otherwise tell the caller only a number.
			"empty message falls back to the status text",
			celerity.NotFound(""), 404, "Not Found",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			res, err := runHandler(s.T(), func(context.Context, getOrder) (string, error) {
				return "", tc.err
			}, &handler.Request{Method: "POST"})

			s.Require().NoError(err, "the pipeline should answer, not fail")
			s.Require().NotNil(res.HTTP)
			s.Equal(tc.wantStatus, res.HTTP.Status)

			var body struct{ Message string }
			s.Require().NoError(json.Unmarshal(res.HTTP.Body, &body))
			s.Equal(tc.wantMessage, body.Message)
		})
	}
}

func (s *ErrorsTestSuite) Test_an_unhandled_error_stays_a_fault() {
	// Answered as a response here, a failure would move no error metric and
	// appear in no trace: the runtime would be told the handler succeeded.
	boom := errors.New("the database is on fire")

	_, err := runHandler(s.T(), func(context.Context, getOrder) (string, error) {
		return "", boom
	}, &handler.Request{Method: "POST"})

	s.Require().Error(err, "the error should be reported as a fault")
	s.ErrorIs(err, boom)
}

func (s *ErrorsTestSuite) Test_a_malformed_body_is_the_callers_mistake() {
	type order struct {
		Total float64 `json:"total"`
	}

	res, err := runHandler(s.T(), func(_ context.Context, in order) (order, error) {
		return in, nil
	}, &handler.Request{Method: "POST", Body: []byte(`{"total": "not a number"`)})

	s.Require().NoError(err)
	s.Equal(400, res.HTTP.Status, "a body that cannot be read is a 400, not a 500")
}

// domainError is an application's own error type opting into a status without
// importing this SDK's constructors.
type domainError struct{ reason string }

func (e domainError) Error() string   { return e.reason }
func (e domainError) StatusCode() int { return 422 }

func (s *ErrorsTestSuite) Test_an_applications_own_error_can_carry_a_status() {
	res, err := runHandler(s.T(), func(context.Context, getOrder) (string, error) {
		return "", domainError{reason: "the order has no lines"}
	}, &handler.Request{Method: "POST"})

	s.Require().NoError(err)
	s.Equal(422, res.HTTP.Status)
}

func (s *ErrorsTestSuite) Test_a_wrapped_cause_is_not_shown_to_the_caller() {
	cause := errors.New(`pq: duplicate key value violates unique constraint "orders_pkey"`)
	wrapped := celerity.WrapWithStatus(409, "that order already exists", cause)

	res, err := runHandler(s.T(), func(context.Context, getOrder) (string, error) {
		return "", wrapped
	}, &handler.Request{Method: "POST"})
	s.Require().NoError(err)

	// The cause belongs in logs, not in the answer: it names internal schema.
	body := string(res.HTTP.Body)
	s.Contains(body, "already exists")
	s.NotContains(body, "orders_pkey", "the cause should be withheld from the caller")

	// Still reachable for logging and for errors.Is.
	s.ErrorIs(wrapped, cause)
	s.True(strings.Contains(wrapped.Error(), "orders_pkey"),
		"Error() should carry the cause for logs")
}
