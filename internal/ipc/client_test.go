package ipc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/internal/ipc"
	pb "github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

func connect(t *testing.T, socket string, config ipc.Config) *ipc.Client {
	t.Helper()

	transport, err := ipc.Dial(testContext(t), ipc.DialConfig{
		Socket:   socket,
		RetryFor: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dialling the runtime: %v", err)
	}
	return ipc.New(transport, config)
}

func TestHandshakeDeclaresWhatTheProcessServes(t *testing.T) {
	runtime := newFakeRuntime()
	socket := runtime.start(t)

	client := connect(t, socket, ipc.Config{
		Tags:        []string{"GET::/orders", "POST::/orders"},
		Resolve:     func(string) (layer.Next, bool) { return nil, false },
		Concurrency: 4,
		Limits:      map[string]int{"POST::/orders": 1},
		SDKVersion:  "test",
	})
	go func() { _ = client.Serve(testContext(t)) }()

	ready := runtime.awaitReady(t)

	// Sorted, so a mismatch reads the same way every time it is reported.
	if got := ready.GetHandlerTags(); len(got) != 2 || got[0] != "GET::/orders" {
		t.Errorf("handler tags = %v, want them sorted", got)
	}
	// The credit window is the worker pool size: throughput saturates there.
	if ready.GetInitialCredit() != 4 {
		t.Errorf("initial credit = %d, want 4", ready.GetInitialCredit())
	}
	if limits := ready.GetLimits(); len(limits) != 1 || limits[0].GetMaxConcurrent() != 1 {
		t.Errorf("limits = %v, want one cap on POST::/orders", limits)
	}
}

func TestHandshakeRefusedWhenTagsDoNotMatchTheBlueprint(t *testing.T) {
	runtime := newFakeRuntime()
	// A mismatch is a startup error rather than a 404 in production.
	runtime.accept = func(*pb.Ready) *pb.ReadyAck {
		return &pb.ReadyAck{
			Accepted:      false,
			UnknownTags:   []string{"GET::/typo"},
			UnhandledTags: []string{"GET::/orders"},
		}
	}
	socket := runtime.start(t)

	client := connect(t, socket, ipc.Config{
		Tags:    []string{"GET::/typo"},
		Resolve: func(string) (layer.Next, bool) { return nil, false },
	})

	err := client.Serve(testContext(t))

	var mismatch *ipc.TagMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Serve returned %v, want a TagMismatchError", err)
	}
	// Both directions are reported: a tag the handler does not serve means
	// events dispatched nowhere, and one the blueprint does not declare means a
	// handler that can never be addressed.
	if len(mismatch.Unknown) != 1 || len(mismatch.Unhandled) != 1 {
		t.Errorf("mismatch = %+v, want one tag in each direction", mismatch)
	}
	for _, want := range []string{"GET::/typo", "GET::/orders"} {
		if !contains(err.Error(), want) {
			t.Errorf("error does not name %q:\n%s", want, err)
		}
	}
}

func TestDispatchIsHandledAndReturnsCredit(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(&pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Dispatch{
			Dispatch: &pb.Dispatch{
				Id:         "event-1",
				HandlerTag: "GET::/orders",
				Source: &pb.Dispatch_Http{Http: &pb.HttpRequest{
					Method: "GET",
					Path:   "/orders",
					Route:  "/orders",
				}},
			},
		}})
	}
	socket := runtime.start(t)

	client := connect(t, socket, ipc.Config{
		Tags: []string{"GET::/orders"},
		Resolve: func(tag string) (layer.Next, bool) {
			return func(_ context.Context, ev *handler.Event) (*handler.Result, error) {
				return &handler.Result{ID: ev.ID, HTTP: &handler.Response{
					Status: 200,
					Body:   []byte(`{"orders":[]}`),
				}}, nil
			}, true
		},
		Concurrency: 2,
	})
	go func() { _ = client.Serve(testContext(t)) }()

	result := runtime.awaitResult(t)

	if result.GetId() != "event-1" {
		t.Errorf("result id = %q, want event-1", result.GetId())
	}
	if result.GetHttp().GetStatus() != 200 {
		t.Errorf("status = %d, want 200", result.GetHttp().GetStatus())
	}
	if result.GetCreditGrant() != 1 {
		t.Errorf("credit grant = %d, want 1", result.GetCreditGrant())
	}
}

func TestCreditIsReturnedWhenAHandlerPanics(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(&pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Dispatch{
			Dispatch: &pb.Dispatch{
				Id:         "event-panic",
				HandlerTag: "GET::/boom",
				Source:     &pb.Dispatch_Http{Http: &pb.HttpRequest{Method: "GET", Path: "/boom"}},
			},
		}})
	}
	socket := runtime.start(t)

	client := connect(t, socket, ipc.Config{
		Tags: []string{"GET::/boom"},
		Resolve: func(string) (layer.Next, bool) {
			return func(context.Context, *handler.Event) (*handler.Result, error) {
				panic("handler exploded")
			}, true
		},
	})
	go func() { _ = client.Serve(testContext(t)) }()

	result := runtime.awaitResult(t)

	// A missed grant drains the window and stalls the stream with nothing
	// reported anywhere, so it has to survive a panic.
	if result.GetCreditGrant() != 1 {
		t.Errorf("credit grant = %d after a panic, want 1", result.GetCreditGrant())
	}
	if result.GetError() == nil {
		t.Fatal("a panic did not become a handler error")
	}
	if !contains(result.GetError().GetMessage(), "handler exploded") {
		t.Errorf("error message = %q, want the panic value", result.GetError().GetMessage())
	}
}

func TestUnroutableTagIsReportedRatherThanDropped(t *testing.T) {
	runtime := newFakeRuntime()
	runtime.afterReady = func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(&pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Dispatch{
			Dispatch: &pb.Dispatch{
				Id:         "event-lost",
				HandlerTag: "GET::/nothing-serves-this",
				Source:     &pb.Dispatch_Http{Http: &pb.HttpRequest{Method: "GET"}},
			},
		}})
	}
	socket := runtime.start(t)

	client := connect(t, socket, ipc.Config{
		Tags:    []string{"GET::/orders"},
		Resolve: func(string) (layer.Next, bool) { return nil, false },
	})
	go func() { _ = client.Serve(testContext(t)) }()

	result := runtime.awaitResult(t)

	// Dropping it would spend a credit and never return it.
	if result.GetCreditGrant() != 1 {
		t.Errorf("credit grant = %d, want 1", result.GetCreditGrant())
	}
	if result.GetError() == nil {
		t.Fatal("an unroutable dispatch produced no error result")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
