package serverless

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Environment variables a Celerity deployment sets on each function, naming the
// one handler that function serves.
const (
	HandlerIDEnvVar  = "CELERITY_HANDLER_ID"
	HandlerTagEnvVar = "CELERITY_HANDLER_TAG"
)

// ErrNoHandler is returned when an event reaches a function that serves no
// handler for it.
var ErrNoHandler = errors.New("celerity: no handler for event")

// Serve builds the invoker for an adapter and hands control to the provider's
// event loop. It does not return until the process is shutting down.
func Serve(ctx context.Context, adapter Adapter, resolver Resolver) error {
	return adapter.Start(ctx, NewInvoker(adapter, resolver))
}

// NewInvoker builds the invoker an adapter's event loop calls once per
// invocation.
//
// Resolution happens on the first invocation and is cached for the life of the
// execution environment, so a warm invocation does no lookup.
func NewInvoker(adapter Adapter, resolver Resolver) Invoker {
	d := &dispatcher{
		mapper:     adapter.Mapper(),
		adapter:    adapter,
		resolver:   resolver,
		handlerID:  os.Getenv(HandlerIDEnvVar),
		handlerTag: os.Getenv(HandlerTagEnvVar),
	}
	return d.invoke
}

type dispatcher struct {
	mapper   EventMapper
	adapter  Adapter
	resolver Resolver

	handlerID  string
	handlerTag string

	once       sync.Once
	resolved   *Handler
	resolveErr error
}

func (d *dispatcher) invoke(ctx context.Context, payload []byte) (any, error) {
	kind, err := d.mapper.Detect(payload)
	if err != nil {
		return nil, fmt.Errorf("detecting event source: %w", err)
	}

	d.once.Do(func() { d.resolved, d.resolveErr = d.resolve(kind) })
	if d.resolveErr != nil {
		return d.noHandler(kind, d.resolveErr)
	}

	ev, err := d.toEvent(kind, payload)
	if err != nil {
		return nil, err
	}

	if sender, ok := d.webSocketSender(ctx, kind, payload); ok {
		ctx = WithWebSocketSender(ctx, sender)
	}

	res, err := d.resolved.Invoke(ctx, ev)
	if err != nil {
		return nil, err
	}
	return d.fromResult(kind, res)
}

// resolve finds the one handler this function serves: by the id the deployment
// set, then by the tag, then by there being exactly one handler of this kind,
// which is the ordinary case since each handler gets its own function.
func (d *dispatcher) resolve(kind handler.Kind) (*Handler, error) {
	if d.handlerID != "" {
		if h, ok := d.resolver.ByName(d.handlerID); ok {
			return h, nil
		}
	}
	if d.handlerTag != "" {
		if h, ok := d.resolver.ByTag(d.handlerTag); ok {
			return h, nil
		}
	}
	if h, ok := d.resolver.Only(kind); ok {
		return h, nil
	}
	return nil, fmt.Errorf(
		"%w: kind %q, %s=%q, %s=%q",
		ErrNoHandler, kind, HandlerIDEnvVar, d.handlerID, HandlerTagEnvVar, d.handlerTag,
	)
}

// noHandler answers an unroutable event, and what that means differs by source.
//
// HTTP and WebSocket get a client-visible status: an unrouted request is an
// answer, not an operational failure. Consumer and schedule raise an error
// instead, because the quiet answer is the wrong one: reporting no batch item
// failures tells the queue every message was handled and it drains silently,
// and returning a value from a schedule makes the invocation a success, so a
// schedule that reaches nothing looks like one that is running.
func (d *dispatcher) noHandler(kind handler.Kind, cause error) (any, error) {
	switch kind {
	case handler.KindHTTP:
		return d.mapper.FromResponse(&handler.Response{
			Status:  404,
			Headers: handler.Params{"content-type": {"application/json"}},
			Body:    []byte(`{"message":"Not Found"}`),
		})
	case handler.KindWebSocket:
		return map[string]int{"statusCode": 404}, nil
	case handler.KindCustom:
		return d.mapper.FromCustomResult(&handler.CustomInvokeResult{})
	default:
		return nil, cause
	}
}

func (d *dispatcher) webSocketSender(
	ctx context.Context,
	kind handler.Kind,
	payload []byte,
) (handler.WebSocketSender, bool) {
	if kind != handler.KindWebSocket {
		return nil, false
	}
	provider, ok := d.adapter.(WebSocketSenderProvider)
	if !ok {
		return nil, false
	}
	sender, err := provider.WebSocketSender(ctx, payload)
	if err != nil {
		return nil, false
	}
	return sender, true
}
