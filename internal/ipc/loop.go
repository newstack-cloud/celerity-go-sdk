package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// loop reads frames from the runtime and dispatches them, until the stream
// closes, the runtime drains, or the context is cancelled.
func (c *Client) loop(ctx context.Context) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var workers sync.WaitGroup
	sendDone := c.startSender(ctx)

	defer func() {
		workers.Wait()
		close(c.out)
		<-sendDone
		_ = c.transport.Close()
	}()

	for {
		frame, err := c.transport.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("reading from the runtime: %w", err)
		}

		switch {
		case frame.Dispatch != nil:
			c.startDispatch(ctx, &workers, frame.Dispatch)
		case frame.Cancel != nil:
			c.cancel(frame.Cancel.ID)
		case frame.WsAck != nil:
			c.deliverWsAck(frame.WsAck)
		case frame.Drain != nil:
			return c.drain(&workers, frame.Drain.Deadline)
		}
	}
}

// startSender serialises every frame the handler sends. One goroutine owns the
// transport's send side, so a result and a WebSocket send from two handlers
// cannot interleave on the wire.
func (c *Client) startSender(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for frame := range c.out {
			if err := c.transport.Send(frame); err != nil {
				telemetry.LoggerFrom(ctx).Error("celerity: sending to the runtime failed",
					"error", err)
				return
			}
		}
	}()
	return done
}

func (c *Client) startDispatch(ctx context.Context, workers *sync.WaitGroup, ev *handler.Event) {
	eventCtx, stop := c.eventContext(ctx, ev)
	c.track(ev.ID, stop)

	workers.Add(1)
	go func() {
		defer workers.Done()
		defer stop()
		defer c.untrack(ev.ID)

		outcome := c.resultFor(eventCtx, ev)
		c.out <- &ToRuntime{Result: &Result{Outcome: outcome, CreditGrant: 1}}
	}()
}

// eventContext applies the runtime's deadline and trace context, so a handler
// that respects ctx respects both without being told about either.
func (c *Client) eventContext(
	ctx context.Context,
	ev *handler.Event,
) (context.Context, context.CancelFunc) {
	if len(ev.TraceContext) > 0 {
		ctx = telemetry.WithTraceContext(ctx, ev.TraceContext)
	}
	ctx = withWebSocketSender(ctx, c)

	if ev.Deadline.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, ev.Deadline)
}

// drain stops taking new work and waits for what is in flight, bounded by the
// runtime's deadline. Work still running when it passes is abandoned.
func (c *Client) drain(workers *sync.WaitGroup, deadline time.Time) error {
	finished := make(chan struct{})
	go func() {
		workers.Wait()
		close(finished)
	}()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case <-finished:
		return nil
	case <-timer.C:
		return nil
	}
}

// Draining tells the runtime this process is going away, so a supervisor can
// roll handler processes without dropping work.
func (c *Client) Draining(deadline time.Time) {
	c.out <- &ToRuntime{Draining: &Draining{Deadline: deadline}}
}

// Grant returns credit outside of a result, for resizing the window or resuming
// after a deliberate withhold.
func (c *Client) Grant(additional uint32) {
	c.out <- &ToRuntime{Credit: &CreditGrant{Additional: additional}}
}
