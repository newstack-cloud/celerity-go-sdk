package aws_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

// A partial failure has to name exactly which messages failed: re-sending a
// whole batch redelivers the ones that did arrive, and whether a client can
// tell those apart depends on the message id.
type SenderTestSuite struct {
	suite.Suite
}

func TestSenderTestSuite(t *testing.T) {
	suite.Run(t, new(SenderTestSuite))
}

func (s *SenderTestSuite) Test_every_message_is_pushed_to_its_connection() {
	var mu sync.Mutex
	sent := map[string]string{}

	sender := awsadapter.SenderWith(func(connectionID string, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		sent[connectionID] = string(data)
		return nil
	})

	err := sender.Send(context.Background(),
		handler.OutboundMessage{ConnectionID: "conn-1", Message: []byte("one")},
		handler.OutboundMessage{ConnectionID: "conn-2", Message: []byte("two")},
	)

	s.Require().NoError(err)
	s.Equal(map[string]string{"conn-1": "one", "conn-2": "two"}, sent)
}

func (s *SenderTestSuite) Test_a_partial_failure_names_the_messages_that_failed() {
	sender := awsadapter.SenderWith(func(connectionID string, _ []byte) error {
		if connectionID == "conn-2" {
			return errors.New("GoneException: connection is gone")
		}
		return nil
	})

	err := sender.Send(context.Background(),
		handler.OutboundMessage{ConnectionID: "conn-1", Message: []byte("one")},
		handler.OutboundMessage{ConnectionID: "conn-2", Message: []byte("two")},
		handler.OutboundMessage{ConnectionID: "conn-3", Message: []byte("three")},
	)

	var sendErr *handler.SendError
	s.Require().ErrorAs(err, &sendErr)
	s.Require().Len(sendErr.Failures, 1)
	// By index, so a caller retries exactly those.
	s.Equal(1, sendErr.Failures[0].Index)
	s.Equal("conn-2", sendErr.Failures[0].ConnectionID)
	s.Contains(sendErr.Failures[0].Error, "connection is gone")

	s.False(sendErr.Failed(0))
	s.True(sendErr.Failed(1))
	s.False(sendErr.Failed(2))
}

func (s *SenderTestSuite) Test_a_send_with_nothing_in_it_reaches_the_platform_not_at_all() {
	called := false
	sender := awsadapter.SenderWith(func(string, []byte) error {
		called = true
		return nil
	})

	s.Require().NoError(sender.Send(context.Background()))
	s.False(called)
}

func (s *SenderTestSuite) Test_the_sender_carries_text_frames_only() {
	// API Gateway disconnects a client that sends a binary frame, so an
	// application asking whether the capability is there is told no rather
	// than having a send fail.
	sender := awsadapter.SenderWith(func(string, []byte) error { return nil })

	_, ok := sender.(handler.BinarySender)
	s.False(ok)
}
