package aws_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

// The whole path, from the payload Lambda delivers to what it serialises back,
// through a real application rather than a stand-in resolver where the mapper suites
// cover translation, and this covers that an application's handlers are
// actually reached by it.
type InvokeTestSuite struct {
	suite.Suite
}

func TestInvokeTestSuite(t *testing.T) {
	suite.Run(t, new(InvokeTestSuite))
}

type createOrder struct {
	OrderID string `path:"orderId"`
	Note    string `json:"note"`
}

type orderCreated struct {
	OrderID string `json:"orderId"`
	Note    string `json:"note"`
}

func (s *InvokeTestSuite) Test_a_request_reaches_the_handler_and_its_answer_comes_back() {
	app := celerity.New()
	celerity.Post(app, "/orders/{orderId}",
		func(_ context.Context, in *createOrder) (*orderCreated, error) {
			return &orderCreated{OrderID: in.OrderID, Note: in.Note}, nil
		})
	s.Require().NoError(app.Err())

	out, err := s.invoke(app, s.request("POST /orders/{orderId}", "/orders/ord_42",
		map[string]string{"orderId": "ord_42"}, `{"note":"ship it"}`))

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	s.Equal(200, res.StatusCode)
	// The path parameter bound over the body, which is the direction a caller
	// cannot spoof.
	s.JSONEq(`{"orderId":"ord_42","note":"ship it"}`, res.Body)
}

func (s *InvokeTestSuite) Test_a_handler_error_becomes_the_status_it_carries() {
	app := celerity.New()
	celerity.Post(
		app, "/orders/{orderId}",
		func(_ context.Context, _ *createOrder) (*orderCreated, error) {
			return nil, &handler.StatusError{
				Status:  409,
				Message: "that order already exists",
			}
		},
	)
	s.Require().NoError(app.Err())

	out, err := s.invoke(app, s.request("POST /orders/{orderId}", "/orders/ord_42",
		map[string]string{"orderId": "ord_42"}, `{"note":"again"}`))

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	s.Equal(409, res.StatusCode)
	s.Contains(res.Body, "that order already exists")
}

func (s *InvokeTestSuite) Test_a_request_no_handler_serves_is_answered_rather_than_failed() {
	app := celerity.New()
	celerity.Get(app, "/health", func(context.Context, *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})
	celerity.Get(app, "/orders", func(context.Context, *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})
	s.Require().NoError(app.Err())

	// Two handlers of the kind and nothing naming which, so nothing resolves.
	out, err := s.invoke(app, s.request("GET /unknown", "/unknown", nil, ""))

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	s.Equal(404, res.StatusCode, "an unrouted request is an answer, not an operational failure")
}

func (s *InvokeTestSuite) Test_the_function_is_told_which_handler_it_serves() {
	app := celerity.New()
	celerity.Get(app, "/health",
		func(context.Context, *struct{}) (*orderCreated, error) {
			return &orderCreated{Note: "health"}, nil
		}, celerity.Named("checkHealth"))
	celerity.Get(app, "/orders",
		func(context.Context, *struct{}) (*orderCreated, error) {
			return &orderCreated{Note: "orders"}, nil
		}, celerity.Named("listOrders"))
	s.Require().NoError(app.Err())

	// Which is how a deployment that gives each handler its own function
	// addresses the one this function is.
	s.T().Setenv(serverless.HandlerIDEnvVar, "listOrders")

	out, err := s.invoke(app, s.request("GET /orders", "/orders", nil, ""))

	s.Require().NoError(err)
	res, ok := out.(events.APIGatewayV2HTTPResponse)
	s.Require().True(ok)
	s.Contains(res.Body, "orders")
}

func (s *InvokeTestSuite) Test_a_batch_reaches_a_consumer_and_a_failure_is_reported_per_message() {
	app := celerity.New()
	celerity.Consume(app, "ordersQueue",
		func(_ context.Context, batch *handler.ConsumerBatch) (*handler.BatchResult, error) {
			res := &handler.BatchResult{}
			for _, record := range batch.Records {
				if record.MessageID == "msg-2" {
					res.Fail(record.MessageID, assertionError("the pricing service is down"))
				}
			}
			return res, nil
		})
	s.Require().NoError(app.Err())

	out, err := s.invoke(app, fixture(&s.Suite, "sqs.json"))

	s.Require().NoError(err)
	res, ok := out.(events.SQSEventResponse)
	s.Require().True(ok)
	s.Require().Len(res.BatchItemFailures, 1)
	// Only that message is left on the queue; the rest of the batch is deleted.
	s.Equal("msg-2", res.BatchItemFailures[0].ItemIdentifier)
}

func (s *InvokeTestSuite) Test_a_batch_no_consumer_serves_fails_the_invocation() {
	app := celerity.New()
	celerity.Get(app, "/health", func(context.Context, *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})
	s.Require().NoError(app.Err())

	_, err := s.invoke(app, fixture(&s.Suite, "sqs.json"))

	// Reporting no batch item failures would tell SQS every message was handled
	// and the queue would drain silently.
	s.Require().ErrorIs(err, serverless.ErrNoHandler)
}

func (s *InvokeTestSuite) Test_a_message_reaches_a_websocket_handler_by_its_route() {
	app := celerity.New()
	celerity.OnMessage(app, "sendMessage",
		func(ctx context.Context, in *struct {
			Text string `json:"text"`
		}) (*struct{}, error) {
			s.Equal("conn-77", celerity.ConnectionID(ctx))
			return nil, nil
		})
	s.Require().NoError(app.Err())

	// A body that asks for no acknowledgement, so this case is about routing
	// rather than about the receipt, acknowledging one pushes to the platform,
	// which is covered against a recording sender instead.
	out, err := s.invoke(app, s.message("sendMessage", `{"event":"sendMessage","text":"hello"}`))

	s.Require().NoError(err)
	s.Equal(map[string]int{"statusCode": 200}, out)
}

func (s *InvokeTestSuite) message(routeKey, body string) []byte {
	payload, err := json.Marshal(events.APIGatewayWebsocketProxyRequest{
		Body: body,
		RequestContext: events.APIGatewayWebsocketProxyRequestContext{
			RouteKey:     routeKey,
			EventType:    "MESSAGE",
			ConnectionID: "conn-77",
			RequestID:    "ws-req-5",
			DomainName:   "ws123.execute-api.eu-west-2.amazonaws.com",
			Stage:        "live",
		},
	})
	s.Require().NoError(err)
	return payload
}

// invoke runs one payload through the adapter against a real application, the
// way the Lambda runtime does.
func (s *InvokeTestSuite) invoke(app *celerity.App, payload []byte) (any, error) {
	adapter := awsadapter.New()
	return serverless.NewInvoker(adapter, app.Resolver())(context.Background(), payload)
}

func (s *InvokeTestSuite) request(routeKey, path string, params map[string]string, body string) []byte {
	payload, err := json.Marshal(events.APIGatewayV2HTTPRequest{
		RouteKey:       routeKey,
		RawPath:        path,
		PathParameters: params,
		Headers:        map[string]string{"content-type": "application/json"},
		Body:           body,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			RequestID: "req-1",
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: firstWord(routeKey),
				Path:   path,
			},
		},
	})
	s.Require().NoError(err)
	return payload
}

func firstWord(s string) string {
	for i := range len(s) {
		if s[i] == ' ' {
			return s[:i]
		}
	}
	return s
}
