package ipc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/internal/ipc"
	pb "github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// The WebSocket side channel shares the one stream with everything else, so a
// send has to be matched to its acknowledgement by correlation id. These cover
// that matching, and what a handler is told when delivery partly fails.
type WebSocketSendTestSuite struct {
	suite.Suite
}

func TestWebSocketSendTestSuite(t *testing.T) {
	suite.Run(t, new(WebSocketSendTestSuite))
}

// wsHandlerSending returns a handler that pushes the given messages and records
// what Send answered.
func wsHandlerSending(
	messages []handler.OutboundMessage,
	result chan<- error,
) func(string) (layer.Next, bool) {
	return func(string) (layer.Next, bool) {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			sender, ok := serverless.WebSocketSenderFrom(ctx)
			if !ok {
				result <- errors.New("no websocket sender in the handler's context")
				return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
			}
			result <- sender.Send(ctx, messages...)
			return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
		}, true
	}
}

// wsHandlerSendingBinary returns a handler that pushes binary messages through
// the sender's optional binary capability and records what it answered.
func wsHandlerSendingBinary(
	messages []handler.OutboundBinaryMessage,
	result chan<- error,
) func(string) (layer.Next, bool) {
	return func(string) (layer.Next, bool) {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			sender, ok := serverless.WebSocketSenderFrom(ctx)
			if !ok {
				result <- errors.New("no websocket sender in the handler's context")
				return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
			}
			binary, ok := sender.(handler.BinarySender)
			if !ok {
				result <- errors.New("the runtime's sender should carry binary frames")
				return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
			}
			result <- binary.SendBinary(ctx, messages...)
			return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
		}, true
	}
}

// serveOne dispatches a single WebSocket message to the given handler.
func (s *WebSocketSendTestSuite) serveOne(
	runtime *fakeRuntime,
	resolve func(string) (layer.Next, bool),
) {
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(dispatchWebSocket("event-1", "$default::$default"))
	}
	s.serve(runtime, resolve)
}

func (s *WebSocketSendTestSuite) serve(
	runtime *fakeRuntime,
	resolve func(string) (layer.Next, bool),
) {
	client := connect(s.T(), runtime.start(s.T()), ipc.Config{
		Tags:        []string{"$default::$default"},
		Resolve:     resolve,
		Concurrency: 4,
	})
	go func() { _ = client.Serve(testContext(s.T())) }()
}

// awaitSend returns what Send answered, failing rather than hanging.
func (s *WebSocketSendTestSuite) awaitSend(result <-chan error) error {
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		s.FailNow("Send never returned, so the acknowledgement did not reach it")
		return nil
	}
}

func (s *WebSocketSendTestSuite) Test_a_send_is_delivered_and_acknowledged() {
	runtime := newFakeRuntime()

	sendResult := make(chan error, 1)
	s.serveOne(runtime, wsHandlerSending([]handler.OutboundMessage{
		{ConnectionID: "conn-1", Message: []byte("hello")},
	}, sendResult))

	send := runtime.awaitWsSend(s.T())

	s.NotEmpty(send.GetCorrelationId(),
		"without a correlation id no acknowledgement could match the send")
	s.Require().Len(send.GetMessages(), 1)
	s.Equal("conn-1", send.GetMessages()[0].GetConnectionId())

	s.NoError(s.awaitSend(sendResult))
}

// Everything the runtime does for an acknowledgement, waiting for the client,
// sending again while attempts remain and declaring the message lost, happens
// only if the request reaches it.
func (s *WebSocketSendTestSuite) Test_a_requested_acknowledgement_is_carried_per_message() {
	runtime := newFakeRuntime()

	sendResult := make(chan error, 1)
	s.serveOne(runtime, wsHandlerSending([]handler.OutboundMessage{
		{ConnectionID: "conn-1", Message: []byte("hello"), WaitForAck: true},
		{ConnectionID: "conn-2", Message: []byte("hello")},
	}, sendResult))

	messages := runtime.awaitWsSend(s.T()).GetMessages()

	s.Require().Len(messages, 2)
	s.True(messages[0].GetWaitForAck(), "the message that asked for an acknowledgement lost it")
	s.False(messages[1].GetWaitForAck(), "a message that asked for nothing carried a request")

	s.awaitSend(sendResult)
}

func (s *WebSocketSendTestSuite) Test_a_send_reports_which_messages_failed() {
	runtime := newFakeRuntime()
	// Failures are per message so a handler can retry exactly those. Re-sending
	// a whole batch would redeliver the messages that did arrive.
	runtime.answerWsSend = func(*pb.WsSend) *pb.WsSendAck {
		return &pb.WsSendAck{
			Success: false,
			Failures: []*pb.WsSendFailure{
				{Index: 1, ConnectionId: "conn-b", ErrorMessage: "gone"},
			},
		}
	}

	sendResult := make(chan error, 1)
	s.serveOne(runtime, wsHandlerSending([]handler.OutboundMessage{
		{ConnectionID: "conn-a", Message: []byte("a")},
		{ConnectionID: "conn-b", Message: []byte("b")},
		{ConnectionID: "conn-c", Message: []byte("c")},
	}, sendResult))

	err := s.awaitSend(sendResult)

	var sendErr *handler.SendError
	s.Require().ErrorAs(err, &sendErr)
	s.Require().Len(sendErr.Failures, 1)
	s.Equal("conn-b", sendErr.Failures[0].ConnectionID)

	// By index, so a caller can retry exactly what failed rather than the batch.
	s.True(sendErr.Failed(1), "the failing message should be identified by index")
	s.False(sendErr.Failed(0), "a delivered message should be reported as delivered")
	s.False(sendErr.Failed(2), "a delivered message should be reported as delivered")
}

func (s *WebSocketSendTestSuite) Test_concurrent_sends_each_get_their_own_acknowledgement() {
	// One stream carries every send, so without correlation two handlers in
	// flight could take each other's acknowledgement.
	runtime := newFakeRuntime()
	runtime.answerWsSend = func(send *pb.WsSend) *pb.WsSendAck {
		// Fails only the batch from the second connection, so an acknowledgement
		// delivered to the wrong waiter is visible as the wrong verdict.
		if send.GetMessages()[0].GetConnectionId() == "conn-2" {
			return &pb.WsSendAck{Failures: []*pb.WsSendFailure{{Index: 0, ConnectionId: "conn-2"}}}
		}
		return &pb.WsSendAck{Success: true}
	}
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		for _, id := range []string{"event-1", "event-2"} {
			if err := stream.Send(dispatchWebSocket(id, "$default::$default")); err != nil {
				return err
			}
		}
		return nil
	}

	var mu sync.Mutex
	verdicts := map[string]error{}
	done := make(chan struct{}, 2)

	s.serve(runtime, func(string) (layer.Next, bool) {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			sender, _ := serverless.WebSocketSenderFrom(ctx)
			connection := "conn-1"
			if ev.ID == "event-2" {
				connection = "conn-2"
			}
			err := sender.Send(ctx, handler.OutboundMessage{
				ConnectionID: connection,
				Message:      []byte("x"),
			})

			mu.Lock()
			verdicts[connection] = err
			mu.Unlock()
			done <- struct{}{}

			return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
		}, true
	})

	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			s.FailNow("both sends did not complete")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	s.NoError(verdicts["conn-1"], "conn-1 should get the acknowledgement for its own send")
	s.Error(verdicts["conn-2"], "conn-2 should get the failure that was meant for it")
}

func (s *WebSocketSendTestSuite) Test_a_send_gives_up_when_the_context_is_cancelled() {
	runtime := newFakeRuntime()
	// Never acknowledged, which is what a runtime that has stopped answering
	// looks like from here.
	runtime.answerWsSend = func(*pb.WsSend) *pb.WsSendAck { return nil }

	sendResult := make(chan error, 1)
	s.serveOne(runtime, func(string) (layer.Next, bool) {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
			defer cancel()

			sender, _ := serverless.WebSocketSenderFrom(ctx)
			sendResult <- sender.Send(ctx, handler.OutboundMessage{ConnectionID: "conn-1"})
			return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: true}}, nil
		}, true
	})

	// Blocking forever would hold a worker and the credit it spent.
	s.ErrorIs(s.awaitSend(sendResult), context.DeadlineExceeded)
}

func (s *WebSocketSendTestSuite) Test_send_binary_frames_every_message_in_the_batch() {
	runtime := newFakeRuntime()

	sendResult := make(chan error, 1)
	s.serveOne(runtime, wsHandlerSendingBinary([]handler.OutboundBinaryMessage{
		{ConnectionID: "conn-1", Route: "a", Message: []byte{0x01}},
		{
			ConnectionID:   "conn-2",
			Route:          "b",
			FrameMessageID: "m-1",
			RequireAck:     true,
			Message:        []byte{0x02},
		},
	}, sendResult))

	messages := runtime.awaitWsSend(s.T()).GetMessages()
	s.Require().Len(messages, 2)

	// [routeLength][route][requireAck][messageIdLength][messageId][payload]
	s.Equal([]byte{0x01, 'a', 0x0, 0x0, 0x01}, messages[0].GetMessage())
	s.Equal([]byte{0x01, 'b', 0x1, 0x3, 'm', '-', '1', 0x02}, messages[1].GetMessage())

	for i, m := range messages {
		s.True(m.GetIsBinary(),
			"message %d did not go out as binary, so a client reads the frame as text", i)
	}
}

// Unlike a send that fails on the way, a frame that cannot be composed is a
// mistake in the calling code. Sending the messages before it would leave the
// client a partial batch to make sense of while the caller fixes a bug.
func (s *WebSocketSendTestSuite) Test_send_binary_sends_nothing_when_a_message_cannot_be_framed() {
	runtime := newFakeRuntime()

	sendResult := make(chan error, 1)
	s.serveOne(runtime, wsHandlerSendingBinary([]handler.OutboundBinaryMessage{
		{ConnectionID: "conn-1", Route: "a", Message: []byte{0x01}},
		// A route beginning with a reserved byte: a client would read it as one
		// of the protocol's own messages rather than as the route it was meant
		// to be.
		{ConnectionID: "conn-2", Route: "\x04ck", Message: []byte{0x02}},
	}, sendResult))

	s.ErrorIs(s.awaitSend(sendResult), handler.ErrRouteReserved)

	select {
	case send := <-runtime.wsSent:
		s.Failf("a partial batch went out",
			"%d message(s) were sent despite another in the batch being unframeable",
			len(send.GetMessages()))
	case <-time.After(200 * time.Millisecond):
	}
}
