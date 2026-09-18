package handler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

type WebSocketSenderTestSuite struct {
	suite.Suite
}

func TestWebSocketSenderTestSuite(t *testing.T) {
	suite.Run(t, new(WebSocketSenderTestSuite))
}

// Binary is not on WebSocketSender, so a handler asks whether the sender it was
// given has the capability. The assertion is the point: on a deploy target
// whose transport carries text frames only it fails, where a field on
// OutboundMessage would have compiled and then done nothing useful.
func (s *WebSocketSenderTestSuite) Test_a_text_only_sender_does_not_satisfy_BinarySender() {
	var textOnly handler.WebSocketSender = stubTextSender{}

	_, ok := textOnly.(handler.BinarySender)
	s.False(ok)
}

func (s *WebSocketSenderTestSuite) Test_a_binary_transport_satisfies_BinarySender() {
	var full handler.WebSocketSender = stubBinarySender{}

	_, ok := full.(handler.BinarySender)
	s.True(ok)
}

type stubTextSender struct{}

func (stubTextSender) Send(context.Context, ...handler.OutboundMessage) error { return nil }

type stubBinarySender struct{ stubTextSender }

func (stubBinarySender) SendBinary(context.Context, ...handler.OutboundBinaryMessage) error {
	return nil
}
