package celerity

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

type eventKey struct{}

// WithEvent returns a context carrying the event being handled.
//
// The pipeline does this for every dispatch; a handler does not need to.
func WithEvent(ctx context.Context, ev *handler.Event) context.Context {
	return context.WithValue(ctx, eventKey{}, ev)
}

// EventFrom returns the event being handled, with its source and identifiers.
func EventFrom(ctx context.Context) (*handler.Event, bool) {
	ev, ok := ctx.Value(eventKey{}).(*handler.Event)
	return ev, ok && ev != nil
}

// WebSocketEventFrom returns the WebSocket message being handled.
//
// It is how a handler reaches the connection it should answer on where a WebSocket
// message is acknowledged rather than replied to, so anything the client sees
// is pushed back through [WebSocketSenderFrom] to a connection named here.
func WebSocketEventFrom(ctx context.Context) (*handler.WebSocketMessage, bool) {
	ev, ok := EventFrom(ctx)
	if !ok || ev.WebSocket == nil {
		return nil, false
	}
	return ev.WebSocket, true
}

// ConnectionID returns the WebSocket connection the event arrived on, or the
// empty string for any other source.
func ConnectionID(ctx context.Context) string {
	if ws, ok := WebSocketEventFrom(ctx); ok {
		return ws.ConnectionID
	}
	return ""
}

// RequestID returns the identifier the runtime gave this delivery, whatever the
// source, which is what ties a handler's own logging to the runtime's.
func RequestID(ctx context.Context) string {
	ev, ok := EventFrom(ctx)
	if !ok {
		return ""
	}
	switch {
	case ev.HTTP != nil:
		return ev.HTTP.RequestID
	case ev.WebSocket != nil:
		return ev.WebSocket.RequestID
	default:
		return ev.ID
	}
}
