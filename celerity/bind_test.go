package celerity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A catch-all path parameter arrives as one value per segment, which is the
// case a first-value-wins binder silently truncates.
type BindTestSuite struct {
	suite.Suite
}

func TestBindTestSuite(t *testing.T) {
	suite.Run(t, new(BindTestSuite))
}

type catchAll struct {
	Path     string   `path:"filePath"`
	Segments []string `path:"filePath"`
}

type scalars struct {
	OrderID string `path:"orderId"`
	Limit   int    `query:"limit"`
	Trace   string `header:"x-trace-id"`
}

// Runs a request through the real pipeline and returns what the
// handler was given.
func bindThrough[In any](t require.TestingT, req *handler.Request) In {
	app := celerity.New()

	var bound In
	celerity.Get(app, "/bind", func(_ context.Context, in In) (string, error) {
		bound = in
		return "ok", nil
	})
	require.NoError(t, app.Err(), "registering")

	reg, ok := app.Registry().Get(celerity.HTTPTag("GET", "/bind"))
	require.True(t, ok, "handler was not registered")

	_, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		Kind: handler.KindHTTP,
		HTTP: req,
	})
	require.NoError(t, err, "running the pipeline")

	return bound
}

func (s *BindTestSuite) Test_a_catch_all_binds_to_a_string_and_to_a_slice() {
	got := bindThrough[catchAll](s.T(), &handler.Request{
		Method:     "GET",
		PathParams: handler.Params{"filePath": {"reports", "2026", "q1.pdf"}},
	})

	s.Equal("reports/2026/q1.pdf", got.Path, "the segments should be rejoined")
	s.Equal([]string{"reports", "2026", "q1.pdf"}, got.Segments)
}

func (s *BindTestSuite) Test_a_catch_all_keeps_an_encoded_separator_in_a_segment() {
	// The runtime splits segments before percent-decoding, so a segment holding
	// an encoded separator survives intact. Rejoining into a string cannot
	// preserve that, which is what the slice form is for.
	got := bindThrough[catchAll](s.T(), &handler.Request{
		Method:     "GET",
		PathParams: handler.Params{"filePath": {"reports", "q1/final.pdf"}},
	})

	s.Equal([]string{"reports", "q1/final.pdf"}, got.Segments)
}

func (s *BindTestSuite) Test_each_source_binds_to_its_tagged_field() {
	got := bindThrough[scalars](s.T(), &handler.Request{
		Method:      "GET",
		PathParams:  handler.Params{"orderId": {"order-1"}},
		QueryParams: handler.Params{"limit": {"25"}},
		Headers:     handler.Params{"x-trace-id": {"trace-1"}},
	})

	s.Equal(scalars{OrderID: "order-1", Limit: 25, Trace: "trace-1"}, got)
}

func (s *BindTestSuite) Test_a_path_parameter_cannot_be_spoofed_through_the_body() {
	// Binding runs after decoding, so a caller cannot reach a path parameter
	// by naming it in the body.
	body, err := json.Marshal(map[string]string{"orderId": "spoofed"})
	s.Require().NoError(err)

	got := bindThrough[scalars](s.T(), &handler.Request{
		Method:     "POST",
		Body:       body,
		PathParams: handler.Params{"orderId": {"genuine"}},
	})

	s.Equal("genuine", got.OrderID)
}
