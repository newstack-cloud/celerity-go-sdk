package celerity

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/guard"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// Pipeline builds the full entry point for a registration: guards, then layers
// outermost to innermost, then the handler.
//
// It is exported so that a serverless adapter maintained outside this
// repository composes the same pipeline the in-repo ones do, rather than
// approximating it.
func (a *App) Pipeline(reg *Registration) layer.Next {
	next := layer.Apply(reg.Handler, reg.Layers...)
	if len(reg.Guards) > 0 {
		next = a.guardLayer(reg)(next)
	}
	return a.contextLayer(reg)(next)
}

func (a *App) toServerlessHandler(reg *Registration) *serverless.Handler {
	return &serverless.Handler{
		Tag:    reg.Tag,
		Name:   reg.Name,
		Kind:   reg.Kind,
		Invoke: a.Pipeline(reg),
	}
}

// contextLayer attaches what every handler can reach from its context.
func (a *App) contextLayer(reg *Registration) layer.Layer {
	return func(next layer.Next) layer.Next {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			if a.options.logger != nil {
				ctx = telemetry.WithLogger(ctx, a.options.logger.With(
					"handler", reg.Name,
					"tag", reg.Tag,
					"eventId", ev.ID,
				))
			}
			if len(ev.TraceContext) > 0 {
				ctx = telemetry.WithTraceContext(ctx, ev.TraceContext)
			}
			return next(ctx, ev)
		}
	}
}

// guardLayer runs a handler's guards ahead of it, refusing the event as a
// status the caller understands rather than as an error the handler invents.
//
// Every guard must allow: they are requirements rather than alternatives, so
// adding one can only narrow access.
func (a *App) guardLayer(reg *Registration) layer.Layer {
	return func(next layer.Next) layer.Next {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			req := guardRequestFrom(ev)

			for _, name := range reg.Guards {
				g, ok := a.options.guards[name]
				if !ok {
					return nil, fmt.Errorf(
						"handler %q is protected by guard %q, which is not registered",
						reg.Tag, name,
					)
				}

				decision, err := g(ctx, req)
				if err != nil {
					return nil, fmt.Errorf("guard %q: %w", name, err)
				}
				if !decision.Allowed {
					return refused(ev, decision), nil
				}
				ctx = guard.WithIdentity(ctx, decision)
			}

			return next(ctx, ev)
		}
	}
}

func guardRequestFrom(ev *handler.Event) *guard.Request {
	req := &guard.Request{Kind: ev.Kind}

	switch {
	case ev.HTTP != nil:
		req.Method = ev.HTTP.Method
		req.Route = ev.HTTP.Route
		req.Headers = ev.HTTP.Headers
		req.Query = ev.HTTP.QueryParams
		req.SourceIP = ev.HTTP.SourceIP
		req.RequestID = ev.HTTP.RequestID
	case ev.WebSocket != nil:
		req.Route = ev.WebSocket.Route
		req.ConnectionID = ev.WebSocket.ConnectionID
		req.SourceIP = ev.WebSocket.SourceIP
		req.RequestID = ev.WebSocket.RequestID
	}

	return req
}

// refused turns a guard's denial into the shape the event's source expects.
//
// A refusal is an answer rather than a failure, so it is a result and not an
// error: an error would be a 500 and would move the handler's error metric for
// something that worked exactly as configured.
func refused(ev *handler.Event, decision guard.Decision) *handler.Result {
	reason := decision.Reason
	if reason == "" {
		reason = "Unauthorized"
	}

	switch ev.Kind {
	case handler.KindHTTP:
		return &handler.Result{ID: ev.ID, HTTP: &handler.Response{
			Status:  401,
			Headers: handler.Params{"content-type": {"application/json"}},
			Body:    []byte(fmt.Sprintf(`{"message":%q}`, reason)),
		}}
	case handler.KindWebSocket:
		return &handler.Result{ID: ev.ID, WebSocket: &handler.Ack{Success: false, Error: reason}}
	default:
		return &handler.Result{ID: ev.ID, Error: &handler.Error{
			Message: reason,
			Type:    "Unauthorized",
		}}
	}
}

// WebSocketSenderFrom returns the sender a handler pushes to connected clients
// through.
//
// It is the runtime's IPC side channel under the Celerity runtime and the
// provider's push API under a serverless adapter, and a handler does not need to
// know which. The second return value is false when the event's source has no
// sender, which is every source other than WebSocket.
func WebSocketSenderFrom(ctx context.Context) (handler.WebSocketSender, bool) {
	return serverless.WebSocketSenderFrom(ctx)
}
