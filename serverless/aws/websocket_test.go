package aws_test

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/suite"

	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

type WebSocketTestSuite struct {
	suite.Suite
	mapper awsadapter.Mapper
}

func TestWebSocketTestSuite(t *testing.T) {
	suite.Run(t, new(WebSocketTestSuite))
}

func (s *WebSocketTestSuite) Test_a_gateway_event_becomes_a_message() {
	msg, err := s.mapper.ToWebSocketMessage(fixture(&s.Suite, "websocket.json"))

	s.Require().NoError(err)
	s.Equal("conn-77", msg.ConnectionID)
	s.Equal("198.51.100.4", msg.SourceIP)
	s.Equal("ws-req-5", msg.RequestID)
	s.Contains(string(msg.Message), "hello", "the body reaches the handler as it arrived")
	// API Gateway carries text frames only and disconnects a client that sends
	// a binary one, so nothing arriving here is binary.
	s.False(msg.IsBinary)
}

func (s *WebSocketTestSuite) Test_the_matched_route_key_is_the_route() {
	cases := []struct {
		name     string
		routeKey string
	}{
		// The same three names a handler registers against, because the API's
		// route key and the runtime's are the same field.
		{"a message route", "sendMessage"},
		{"connect", "$connect"},
		{"disconnect", "$disconnect"},
		{"the fallback route", "$default"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			msg, err := s.mapper.ToWebSocketMessage(s.event(tc.routeKey, ""))

			s.Require().NoError(err)
			s.Equal(tc.routeKey, msg.Route)
		})
	}
}

func (s *WebSocketTestSuite) Test_the_message_id_is_the_clients_where_it_gave_one() {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			// So that an acknowledgement and a loss notification name the
			// message by what the client knows it as.
			name: "the client named the message",
			body: `{"event":"sendMessage","messageId":"client-msg-1"}`,
			want: "client-msg-1",
		},
		{
			name: "the client named nothing, so the delivery's own id stands in",
			body: `{"event":"sendMessage"}`,
			want: "ws-req-5",
		},
		{
			name: "a body that is not a JSON object carries no protocol fields",
			body: `just text`,
			want: "ws-req-5",
		},
		{
			name: "no body at all, as on connect",
			body: "",
			want: "ws-req-5",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			msg, err := s.mapper.ToWebSocketMessage(s.event("sendMessage", tc.body))

			s.Require().NoError(err)
			s.Equal(tc.want, msg.MessageID)
			s.Equal("ws-req-5", msg.RequestID, "the delivery's own id is carried separately")
		})
	}
}

func (s *WebSocketTestSuite) Test_the_endpoint_to_push_back_to_comes_from_the_event() {
	endpoint, err := awsadapter.ManagementEndpoint(fixture(&s.Suite, "websocket.json"))

	s.Require().NoError(err)
	// Carried by the event rather than configured, which is what lets one
	// deployment serve several stages and domains.
	s.Equal("https://ws123.execute-api.eu-west-2.amazonaws.com/live", endpoint)
}

func (s *WebSocketTestSuite) Test_an_event_of_the_wrong_shape_is_refused() {
	_, err := s.mapper.ToWebSocketMessage([]byte(`[]`))

	s.Require().Error(err)
	s.Contains(err.Error(), "API Gateway WebSocket")
}

func (s *WebSocketTestSuite) event(routeKey, body string) []byte {
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
