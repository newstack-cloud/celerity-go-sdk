//go:build integration

package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"
)

// Custom handlers, dispatched by the real runtime.
//
// Nothing triggers a custom handler, no route reaches it and no source feeds
// it, so the only way to make the runtime dispatch one is by name, through the
// local invoke endpoint the runtime serves in development. That makes this the
// one handler kind whose dispatch cannot be exercised by driving the
// application's own API.
//
// The endpoint is enabled by the compose file rather than by the image, and it
// is guarded by the platform as well as by its own switch, since it runs any
// handler the blueprint declares with a payload of the caller's choosing.
type RuntimeInvokeTestSuite struct {
	suite.Suite
}

func TestRuntimeInvokeTestSuite(t *testing.T) {
	suite.Run(t, new(RuntimeInvokeTestSuite))
}

// invokeRequest is the endpoint's own shape, not the SDK's.
type invokeRequest struct {
	HandlerName    string `json:"handlerName"`
	InvocationType string `json:"invocationType"`
	Payload        any    `json:"payload,omitempty"`
}

// invokeResponse carries the handler's output as text. A handler that answered
// with bytes that are not text has them base64-encoded and said to be, which
// this suite's handler never does.
type invokeResponse struct {
	Message      string `json:"message"`
	Data         string `json:"data"`
	DataEncoding string `json:"dataEncoding"`
}

type pricingResult struct {
	OrderID string  `json:"orderId"`
	Total   float64 `json:"total"`
}

// invoke calls the endpoint and returns its answer along with the status, so a
// case can assert on a refusal as readily as on a result.
func (s *RuntimeInvokeTestSuite) invoke(req invokeRequest) (int, invokeResponse, []byte) {
	encoded, err := json.Marshal(req)
	s.Require().NoError(err)

	res, err := http.Post(
		runtimeURL()+"/runtime/handlers/invoke",
		"application/json",
		bytes.NewReader(encoded),
	)
	s.Require().NoError(err, "invoking %s", req.HandlerName)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	s.Require().NoError(err)

	var answer invokeResponse
	_ = json.Unmarshal(body, &answer)
	return res.StatusCode, answer, body
}

func (s *RuntimeInvokeTestSuite) Test_a_custom_handler_is_reached_by_name() {
	status, answer, body := s.invoke(invokeRequest{
		HandlerName:    "recalculatePricingHandler",
		InvocationType: "requestResponse",
		Payload:        map[string]any{"orderId": "order-123", "discount": 25},
	})

	s.Require().Equal(http.StatusOK, status, "answered %s", body)
	// Reaching the handler at all means the tag the runtime built from the
	// resource name is the one the SDK declared at the handshake, which is the
	// half of this a stand-in cannot establish.
	s.Empty(answer.DataEncoding, "the handler answered text")

	var got pricingResult
	s.Require().NoError(json.Unmarshal([]byte(answer.Data), &got), "decoding %s", answer.Data)
	s.Equal(pricingResult{OrderID: "order-123", Total: 75}, got)
}

func (s *RuntimeInvokeTestSuite) Test_the_payload_reaches_the_handler_as_its_typed_input() {
	status, answer, body := s.invoke(invokeRequest{
		HandlerName:    "recalculatePricingHandler",
		InvocationType: "requestResponse",
		Payload:        map[string]any{"orderId": "order-9", "discount": 0},
	})

	s.Require().Equal(http.StatusOK, status, "answered %s", body)

	var got pricingResult
	s.Require().NoError(json.Unmarshal([]byte(answer.Data), &got))
	s.Equal("order-9", got.OrderID, "the input decoded into the handler's own type")
	s.Equal(float64(100), got.Total)
}

func (s *RuntimeInvokeTestSuite) Test_validation_applies_to_a_custom_invocation() {
	// The same validator an HTTP handler's input goes through, so a bad payload
	// is answered rather than reaching the handler. A custom invocation has no
	// status code of its own, so the failure arrives as the invocation failing.
	status, _, body := s.invoke(invokeRequest{
		HandlerName:    "recalculatePricingHandler",
		InvocationType: "requestResponse",
		Payload:        map[string]any{"discount": 250},
	})

	s.Require().NotEqual(http.StatusOK, status, "a payload breaking two rules was accepted")
	s.Contains(string(body), "orderId")
	s.Contains(string(body), "discount", "both broken rules should be reported, not only the first")
}

func (s *RuntimeInvokeTestSuite) Test_a_handler_the_blueprint_never_declared_is_refused() {
	// The name to tag mapping comes from the configuration the handler itself
	// was sent, so the runtime cannot accept a name it never told the handler
	// about.
	status, _, body := s.invoke(invokeRequest{
		HandlerName:    "noSuchHandler",
		InvocationType: "requestResponse",
	})

	s.Require().Equal(http.StatusNotFound, status, "answered %s", body)
}

func (s *RuntimeInvokeTestSuite) Test_an_http_handler_is_not_reachable_as_a_custom_one() {
	// The endpoint addresses any declared handler by name and dispatches every
	// invocation as a custom event, so an HTTP handler is reachable by a route
	// it does not serve. The SDK refuses the event rather than running the
	// handler on an empty request, which it would otherwise answer as though
	// the request had simply been empty.
	status, _, body := s.invoke(invokeRequest{
		HandlerName:    "getOrderHandler",
		InvocationType: "requestResponse",
		Payload:        map[string]any{"orderId": "order-123"},
	})

	s.Require().NotEqual(http.StatusOK, status, "answered %s", body)
	s.Contains(string(body), "http", "the refusal should name what the handler serves")
}
