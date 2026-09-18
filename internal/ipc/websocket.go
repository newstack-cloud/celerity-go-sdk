package ipc

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

var correlationCounter atomic.Uint64

// sender pushes messages to WebSocket clients over the stream's side channel.
type sender struct{ client *Client }

func withWebSocketSender(ctx context.Context, c *Client) context.Context {
	return serverless.WithWebSocketSender(ctx, &sender{client: c})
}

// Send delivers messages and blocks until the runtime acknowledges them.
//
// A partial failure returns a [handler.SendError] naming exactly which messages
// failed, by index, so a caller can retry exactly those. Re-sending a whole
// batch would redeliver the messages that did arrive, and whether the client
// can tell those apart depends on the message ID.
func (s *sender) Send(ctx context.Context, messages ...handler.OutboundMessage) error {
	if len(messages) == 0 {
		return nil
	}

	outbound := make([]Outbound, 0, len(messages))
	for _, m := range messages {
		outbound = append(outbound, Outbound{OutboundMessage: m})
	}
	return s.send(ctx, outbound)
}

// SendBinary frames every message and sends it, implementing
// [handler.BinarySender].
//
// Framing happens for the whole call before any of it goes out. A message whose
// parts cannot be represented is a mistake in the calling code, and sending the
// ones before it would leave the client needing to make sense of a partial batch.
func (s *sender) SendBinary(ctx context.Context, messages ...handler.OutboundBinaryMessage) error {
	if len(messages) == 0 {
		return nil
	}

	outbound := make([]Outbound, 0, len(messages))
	for i, m := range messages {
		framed, err := handler.FrameBinaryMessage(m.Route, m.FrameMessageID, m.RequireAck, m.Message)
		if err != nil {
			return fmt.Errorf("framing binary message %d for connection %s: %w", i, m.ConnectionID, err)
		}
		outbound = append(outbound, Outbound{
			OutboundMessage: handler.OutboundMessage{
				ConnectionID:        m.ConnectionID,
				Message:             framed,
				InformClientsOnLoss: m.InformClientsOnLoss,
				MessageID:           m.MessageID,
				Caller:              m.Caller,
				WaitForAck:          m.WaitForAck,
			},
			IsBinary: true,
		})
	}
	return s.send(ctx, outbound)
}

func (s *sender) send(ctx context.Context, messages []Outbound) error {

	correlationID := strconv.FormatUint(correlationCounter.Add(1), 10)
	acks := make(chan *WsSendAck, 1)

	s.client.wsMu.Lock()
	s.client.wsWaiting[correlationID] = acks
	s.client.wsMu.Unlock()

	defer func() {
		s.client.wsMu.Lock()
		delete(s.client.wsWaiting, correlationID)
		s.client.wsMu.Unlock()
	}()

	select {
	case s.client.out <- &ToRuntime{WsSend: &WsSend{
		CorrelationID: correlationID,
		Messages:      messages,
	}}:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case ack := <-acks:
		if ack.Success {
			return nil
		}
		return &handler.SendError{Failures: ack.Failures}
	case <-ctx.Done():
		return fmt.Errorf("waiting for the runtime to acknowledge a websocket send: %w", ctx.Err())
	}
}

// deliverWsAck hands an acknowledgement to whoever is waiting for it.
//
// An acknowledgement for a correlation nobody is waiting on is dropped: the
// caller has already given up, which a timed-out send does.
func (c *Client) deliverWsAck(ack *WsSendAck) {
	c.wsMu.Lock()
	waiting, ok := c.wsWaiting[ack.CorrelationID]
	c.wsMu.Unlock()

	if !ok {
		return
	}
	select {
	case waiting <- ack:
	default:
	}
}
