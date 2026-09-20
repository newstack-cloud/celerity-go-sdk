package aws_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

type AdapterTestSuite struct {
	suite.Suite
}

func TestAdapterTestSuite(t *testing.T) {
	suite.Run(t, new(AdapterTestSuite))
}

func (s *AdapterTestSuite) Test_importing_the_package_registers_the_adapter() {
	// This is what makes selecting a platform an import rather than something
	// the application wires up, and what the handler manifest reports so a
	// deployment to a platform the binary cannot serve is caught at build time.
	s.Contains(serverless.Registered(), "aws-lambda")
}

func (s *AdapterTestSuite) Test_the_adapter_recognises_its_own_environment() {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"running on Lambda", "orders-handler", true},
		{"running anywhere else", "", false},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.T().Setenv(awsadapter.FunctionNameEnvVar, tc.env)

			adapter := awsadapter.New()

			s.Equal(tc.want, adapter.Detect())
		})
	}
}

func (s *AdapterTestSuite) Test_a_sender_is_built_for_the_connections_own_endpoint() {
	adapter := awsadapter.New()

	sender, err := adapter.WebSocketSender(context.Background(), fixture(&s.Suite, "websocket.json"))

	s.Require().NoError(err)
	s.Require().NotNil(sender)

	again, err := adapter.WebSocketSender(context.Background(), fixture(&s.Suite, "websocket.json"))

	s.Require().NoError(err)
	// One sender per endpoint for the life of the execution environment, so the
	// AWS client and its credentials are built once rather than per invocation.
	s.Same(sender, again)
}

func (s *AdapterTestSuite) Test_an_event_with_nowhere_to_push_back_to_is_refused() {
	payload, err := json.Marshal(map[string]any{"requestContext": map[string]any{
		"connectionId": "conn-1",
		"eventType":    "MESSAGE",
	}})
	s.Require().NoError(err)

	adapter := awsadapter.New()
	_, err = adapter.WebSocketSender(context.Background(), payload)

	s.Require().Error(err)
	s.Contains(err.Error(), "no domain or stage")
}

func (s *AdapterTestSuite) Test_a_message_that_asked_to_be_acknowledged_is() {
	sender := &recordingSender{}
	ctx := serverless.WithWebSocketSender(context.Background(), sender)

	adapter := awsadapter.New()
	err := adapter.AcknowledgeReceipt(ctx, &handler.WebSocketMessage{
		ConnectionID: "conn-77",
		Message:      []byte(`{"event":"sendMessage","ack":true,"messageId":"client-msg-1"}`),
	})

	s.Require().NoError(err)
	s.Require().Len(sender.sent, 1)
	s.Equal("conn-77", sender.sent[0].ConnectionID)

	var ack struct {
		Event string `json:"event"`
		Data  struct {
			MessageID string `json:"messageId"`
			Timestamp string `json:"timestamp"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal(sender.sent[0].Message, &ack))
	// The protocol's own key holding its reserved value, rather than the API's
	// configured route key, an acknowledgement is a control message the client
	// recognises, not one the server routes.
	s.Equal("ack", ack.Event)
	s.Equal("client-msg-1", ack.Data.MessageID)
	s.NotEmpty(ack.Data.Timestamp)
}

func (s *AdapterTestSuite) Test_nothing_is_sent_where_the_client_did_not_ask() {
	cases := []struct {
		name string
		body string
	}{
		{"the client asked for nothing", `{"event":"sendMessage"}`},
		{"acknowledgement was declined", `{"event":"sendMessage","ack":false,"messageId":"m1"}`},
		{
			// An acknowledgement names a message, so a request carrying no id
			// asks for one that could not be sent.
			name: "asked, but named no message",
			body: `{"event":"sendMessage","ack":true}`,
		},
		{"a body that is not a JSON object", `plain text`},
		{"no body at all, as on connect", ``},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			sender := &recordingSender{}
			ctx := serverless.WithWebSocketSender(context.Background(), sender)

			adapter := awsadapter.New()
			err := adapter.AcknowledgeReceipt(ctx, &handler.WebSocketMessage{
				ConnectionID: "conn-77",
				Message:      []byte(tc.body),
			})

			s.Require().NoError(err)
			s.Empty(sender.sent)
		})
	}
}

func (s *AdapterTestSuite) Test_an_acknowledgement_with_no_sender_to_send_it_on() {
	adapter := awsadapter.New()
	err := adapter.AcknowledgeReceipt(context.Background(), &handler.WebSocketMessage{
		ConnectionID: "conn-77",
		Message:      []byte(`{"ack":true,"messageId":"client-msg-1"}`),
	})

	s.Require().Error(err)
	s.Contains(err.Error(), "client-msg-1", "the error should name what went unacknowledged")
}

type recordingSender struct {
	sent []handler.OutboundMessage
}

func (r *recordingSender) Send(_ context.Context, messages ...handler.OutboundMessage) error {
	r.sent = append(r.sent, messages...)
	return nil
}
