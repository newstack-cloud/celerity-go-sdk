package serverless

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

type senderKey struct{}

// WithWebSocketSender returns a context carrying the sender for this
// invocation.
//
// Adapters do not call this directly; [NewInvoker] does it when the adapter
// implements [WebSocketSenderProvider].
func WithWebSocketSender(ctx context.Context, s handler.WebSocketSender) context.Context {
	return context.WithValue(ctx, senderKey{}, s)
}

// WebSocketSenderFrom returns the sender for this invocation, if there is one.
//
// Handlers reach it through celerity.WebSocketSenderFrom, which also covers the
// runtime's IPC side channel.
func WebSocketSenderFrom(ctx context.Context) (handler.WebSocketSender, bool) {
	s, ok := ctx.Value(senderKey{}).(handler.WebSocketSender)
	return s, ok
}
