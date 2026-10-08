package telemetry

import (
	"context"
	"sync/atomic"
)

// Flusher hands over whatever telemetry is held but not yet exported.
//
// A batch of spans is held so that exporting is not a network call per span,
// which means a process that stops without flushing loses what it had not sent.
// Two places have to ask for it, a runtime shutting down, and a serverless
// invocation returning, because an execution environment is frozen between
// invocations rather than left running and a batch sitting in one is a batch
// nobody sends.
type Flusher func(ctx context.Context) error

// The flusher this process exports through, installed the way the tracer is and
// for the same reasons.
var flusher atomic.Pointer[Flusher]

// SetFlusher installs what [Flush] calls, replacing anything already installed.
//
// Called from the init of the module that configured an exporter. An
// application exporting telemetry it set up itself installs its own, so that a
// shutdown flushes what the application built rather than nothing.
func SetFlusher(f Flusher) {
	if f == nil {
		flusher.Store(nil)
		return
	}

	flusher.Store(&f)
}

// Flush hands over what is held, and does nothing where nothing is installed.
//
// Nothing rather than an error: an application that exports no telemetry has
// nothing to flush, and that is a typical case rather than a misconfiguration.
func Flush(ctx context.Context) error {
	f := flusher.Load()
	if f == nil {
		return nil
	}
	return (*f)(ctx)
}
