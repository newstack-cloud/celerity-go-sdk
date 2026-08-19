// Package telemetry holds the logging and tracing seams handlers reach through.
//
// The interfaces are here, in core, so that a handler can log and trace without
// core depending on any particular implementation. The OpenTelemetry and
// structured-logging wiring lives behind them.
package telemetry

import (
	"context"
	"log/slog"
)

// Logger is the logging seam.
//
// It is [slog.Handler]-shaped rather than an interface of its own so that any
// slog handler an application already has works unchanged.
type Logger = *slog.Logger

type loggerKey struct{}

// WithLogger returns a context carrying a logger, with the event's identifiers
// already attached by the framework.
func WithLogger(ctx context.Context, l Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// LoggerFrom returns the logger for the event being handled.
//
// It never returns nil: without one it falls back to slog's default, so a
// handler can log unconditionally.
func LoggerFrom(ctx context.Context) Logger {
	if l, ok := ctx.Value(loggerKey{}).(Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// TraceContext carries W3C traceparent and tracestate across the process
// boundary, which is how a trace started upstream of the runtime continues
// through a handler.
type TraceContext map[string]string

const (
	TraceParentHeader = "traceparent"
	TraceStateHeader  = "tracestate"
)

type traceKey struct{}

// WithTraceContext returns a context carrying the trace context a dispatch
// arrived with.
func WithTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceKey{}, tc)
}

// TraceContextFrom returns the trace context the event arrived with.
func TraceContextFrom(ctx context.Context) (TraceContext, bool) {
	tc, ok := ctx.Value(traceKey{}).(TraceContext)
	return tc, ok
}
