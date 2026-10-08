package celerity

import (
	"context"
	"fmt"
	"log/slog"

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

// contextLayer attaches what every handler can reach from its context, and
// opens the span the dispatch is traced as.
//
// Outermost, so the span covers the guards and every layer as well as the
// handler, and so that anything a handler traces is a child of it. A trace that
// started upstream of the runtime continues into it, since the trace context
// the event arrived with is attached before the span is started.
func (a *App) contextLayer(reg *Registration) layer.Layer {
	return func(next layer.Next) layer.Next {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			ctx = WithEvent(ctx, ev)
			if len(ev.TraceContext) > 0 {
				// Before the span is started, so a trace that began upstream
				// of the runtime is the one the dispatch continues.
				ctx = telemetry.WithTraceContext(ctx, ev.TraceContext)
			}
			return a.traced(ctx, reg, ev, next)
		}
	}
}

// Runs the rest of the pipeline inside the dispatch's span, with the
// logger the span's identity is bound into.
//
// The logger is attached after the span is started rather than before, which is
// what lets a handler write a correlated record with logger.Info rather than
// logger.InfoContext: slog's plain methods log against a background context, so
// a handler that reads its logger from the context would otherwise lose the
// span a correlating handler would have read from one.
//
// A result carrying an error is recorded on the span as well as one returned,
// since a handler that answered with a failure is a failed dispatch whichever
// way the failure travelled.
func (a *App) traced(
	ctx context.Context, reg *Registration, ev *handler.Event, next layer.Next,
) (*handler.Result, error) {
	return telemetry.Traced(ctx, dispatchSpan(reg), dispatchAttrs(reg, ev),
		func(ctx context.Context, span telemetry.Span) (*handler.Result, error) {
			ctx = telemetry.WithLogger(ctx, a.dispatchLogger(reg, ev, span))

			result, err := next(ctx, ev)
			if err == nil && result != nil && result.Error != nil {
				return result, nil
			}
			return result, err
		})
}

// The logger a handler writes through, the application's logger
// with what is true of the current dispatch bound into it.
//
// The trace and span are bound where the dispatch is being recorded, which is
// what makes a log record findable from a trace and the reverse. They are left
// off where it is not, rather than written as empty strings that a query would
// have to know to ignore.
func (a *App) dispatchLogger(
	reg *Registration, ev *handler.Event, span telemetry.Span,
) telemetry.Logger {
	logger := a.options.logger
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.With(
		"handler", reg.Name,
		"tag", reg.Tag,
		"eventId", ev.ID,
	)
	if sc := span.Context(); sc.Recorded() {
		logger = logger.With(
			telemetry.TraceIDKey, sc.TraceID, telemetry.SpanIDKey, sc.SpanID,
		)
	}
	return logger
}

// Names a dispatch's span, in the form the other SDKs
// use so that one trace reads the same whichever language served it.
func dispatchSpan(reg *Registration) string {
	return "celerity.handler." + string(reg.Kind)
}

// dispatchAttrs is what every dispatch records, whatever kind it is.
func dispatchAttrs(reg *Registration, ev *handler.Event) []telemetry.Attr {
	attrs := []telemetry.Attr{
		telemetry.String("handler.name", reg.Name),
		telemetry.String("handler.tag", reg.Tag),
		telemetry.String("handler.event_id", ev.ID),
	}
	if ev.HTTP != nil {
		attrs = append(attrs,
			telemetry.String("http.request.method", ev.HTTP.Method),
			telemetry.String("http.route", reg.Route),
		)
	}
	if ev.Consumer != nil {
		attrs = append(attrs, telemetry.Int("handler.batch_size", len(ev.Consumer.Records)))
	}
	return attrs
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
		return &handler.Result{ID: ev.ID, HTTP: handler.JSONMessage(401, reason)}
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
