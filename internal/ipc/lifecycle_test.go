package ipc_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/internal/ipc"
	pb "github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Cancellation, deadlines and drain: what the runtime tells a handler process
// about work it should stop doing, or finish before going away.
type LifecycleTestSuite struct {
	suite.Suite
}

func TestLifecycleTestSuite(t *testing.T) {
	suite.Run(t, new(LifecycleTestSuite))
}

func dispatchHTTP(id, tag string, deadline time.Time) *pb.RuntimeMessage {
	d := &pb.Dispatch{
		Id:         id,
		HandlerTag: tag,
		Source:     &pb.Dispatch_Http{Http: &pb.HttpRequest{Method: "GET", Path: "/work"}},
	}
	if !deadline.IsZero() {
		d.DeadlineUnixMs = deadline.UnixMilli()
	}
	return &pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Dispatch{Dispatch: d}}
}

func (s *LifecycleTestSuite) serve(runtime *fakeRuntime, resolve func(string) (layer.Next, bool)) {
	client := connect(s.T(), runtime.start(s.T()), ipc.Config{
		Tags:    []string{"GET::/work"},
		Resolve: resolve,
	})
	go func() { _ = client.Serve(testContext(s.T())) }()
}

// Fails the test rather than hanging when something never happens.
func (s *LifecycleTestSuite) awaitSignal(signal <-chan struct{}, what string) {
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		s.FailNow(what)
	}
}

func (s *LifecycleTestSuite) Test_cancel_stops_work_nobody_is_waiting_for() {
	runtime := newFakeRuntime()
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(dispatchHTTP("event-1", "GET::/work", time.Time{}))
	}

	started := make(chan struct{})
	cancelled := make(chan struct{})

	s.serve(runtime, func(string) (layer.Next, bool) {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			close(started)
			select {
			case <-ctx.Done():
				close(cancelled)
			case <-time.After(10 * time.Second):
			}
			return &handler.Result{ID: ev.ID, HTTP: &handler.Response{Status: 499}}, nil
		}, true
	})

	s.awaitSignal(started, "the handler never started")
	runtime.push <- &pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Cancel{
		Cancel: &pb.Cancel{Id: "event-1", Reason: pb.Cancel_REASON_CALLER_GONE},
	}}
	s.awaitSignal(cancelled, "the cancellation never reached the handler's context")

	// Still returns a result, and so still returns the credit the dispatch spent.
	s.Equal(uint32(1), runtime.awaitResult(s.T()).GetCreditGrant())
}

func (s *LifecycleTestSuite) Test_a_cancel_for_a_finished_event_is_ignored() {
	runtime := newFakeRuntime()
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(dispatchHTTP("event-1", "GET::/work", time.Time{}))
	}

	s.serve(runtime, func(string) (layer.Next, bool) {
		return func(_ context.Context, ev *handler.Event) (*handler.Result, error) {
			return &handler.Result{ID: ev.ID, HTTP: &handler.Response{Status: 200}}, nil
		}, true
	})

	runtime.awaitResult(s.T())

	// The runtime does not wait to find out whether the handler finished before
	// deciding nobody is waiting for the result, so this arrives for work that
	// is already done and must not be treated as an error.
	runtime.push <- &pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Cancel{
		Cancel: &pb.Cancel{Id: "event-1", Reason: pb.Cancel_REASON_DEADLINE_EXCEEDED},
	}}
	runtime.push <- dispatchHTTP("event-2", "GET::/work", time.Time{})

	// The stream still serves, which is the observable form of "ignored".
	s.Equal("event-2", runtime.awaitResult(s.T()).GetId())
}

func (s *LifecycleTestSuite) Test_the_dispatch_deadline_reaches_the_handlers_context() {
	runtime := newFakeRuntime()
	deadline := time.Now().Add(400 * time.Millisecond)
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(dispatchHTTP("event-1", "GET::/work", deadline))
	}

	observed := make(chan error, 1)
	s.serve(runtime, func(string) (layer.Next, bool) {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			// A handler that respects ctx respects the runtime's deadline
			// without ever being told about it.
			<-ctx.Done()
			observed <- ctx.Err()
			return &handler.Result{ID: ev.ID, HTTP: &handler.Response{Status: 504}}, nil
		}, true
	})

	select {
	case err := <-observed:
		s.ErrorIs(err, context.DeadlineExceeded)
	case <-time.After(5 * time.Second):
		s.FailNow("the dispatch deadline never reached the handler's context")
	}
}

func (s *LifecycleTestSuite) Test_drain_lets_in_flight_work_finish() {
	runtime := newFakeRuntime()
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(dispatchHTTP("event-1", "GET::/work", time.Time{}))
	}

	started := make(chan struct{})
	s.serve(runtime, func(string) (layer.Next, bool) {
		return func(_ context.Context, ev *handler.Event) (*handler.Result, error) {
			close(started)
			// Long enough that a drain which did not wait would close the
			// stream before this returns.
			time.Sleep(300 * time.Millisecond)
			return &handler.Result{ID: ev.ID, HTTP: &handler.Response{Status: 200}}, nil
		}, true
	})

	s.awaitSignal(started, "the handler never started")
	runtime.push <- &pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Drain{
		Drain: &pb.Drain{DeadlineUnixMs: time.Now().Add(5 * time.Second).UnixMilli()},
	}}

	s.Equal(uint32(200), runtime.awaitResult(s.T()).GetHttp().GetStatus(),
		"the in-flight handler should have finished")
}
