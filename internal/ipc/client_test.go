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

// The handshake is where a handler process says what it serves and which
// contract it speaks, and where the runtime refuses it if either disagrees.
type HandshakeTestSuite struct {
	suite.Suite
}

func TestHandshakeTestSuite(t *testing.T) {
	suite.Run(t, new(HandshakeTestSuite))
}

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

// serve dials the stand-in and serves until the test ends.
func (s *HandshakeTestSuite) serve(runtime *fakeRuntime, config ipc.Config) {
	client := connect(s.T(), runtime.start(s.T()), config)
	go func() { _ = client.Serve(testContext(s.T())) }()
}

func (s *HandshakeTestSuite) Test_the_handshake_declares_what_the_process_serves() {
	runtime := newFakeRuntime()

	s.serve(runtime, ipc.Config{
		Tags:        []string{"GET::/orders", "POST::/orders"},
		Resolve:     func(string) (layer.Next, bool) { return nil, false },
		Concurrency: 4,
		Limits:      func() map[string]int { return map[string]int{"POST::/orders": 1} },
		SDKVersion:  "test",
	})

	ready := runtime.awaitReady(s.T())

	// Sorted, so a mismatch reads the same way every time it is reported.
	s.Equal([]string{"GET::/orders", "POST::/orders"}, ready.GetHandlerTags())
	// The credit window is the worker pool size: throughput saturates there.
	s.Equal(uint32(4), ready.GetInitialCredit())

	limits := ready.GetLimits()
	s.Require().Len(limits, 1)
	s.Equal("POST::/orders", limits[0].GetHandlerTag())
	s.Equal(uint32(1), limits[0].GetMaxConcurrent())

	// Required: the runtime refuses a handler that declares no contract
	// version rather than assuming it speaks the current one.
	version := ready.GetProtocolVersion()
	s.Require().NotNil(version, "the runtime refuses a handler that declares no version")
	s.Equal(uint32(ipc.ProtocolMajor), version.GetMajor())
	s.Equal(uint32(ipc.ProtocolMinor), version.GetMinor())
}

// A contract the runtime does not serve is not a fault in the application, and
// reporting it as a tag mismatch would send whoever reads it looking through
// their handlers for something that is not there.
func (s *HandshakeTestSuite) Test_refused_when_the_runtime_does_not_serve_this_contract() {
	runtime := newFakeRuntime()
	runtime.config = &pb.RuntimeConfig{
		ProtocolVersion: &pb.ProtocolVersion{Major: ipc.ProtocolMajor + 1},
	}
	runtime.accept = func(*pb.Ready) *pb.ReadyAck {
		return &pb.ReadyAck{
			Accepted:      false,
			RefusedReason: pb.ReadyAck_REFUSED_REASON_PROTOCOL_VERSION,
		}
	}

	client := connect(s.T(), runtime.start(s.T()), ipc.Config{
		Tags:    []string{"GET::/orders"},
		Resolve: func(string) (layer.Next, bool) { return nil, false },
	})

	err := client.Serve(testContext(s.T()))

	var refused *ipc.ProtocolVersionError
	s.Require().ErrorAs(err, &refused)
	s.Equal(uint32(ipc.ProtocolMajor), refused.Declared.Major,
		"declared should be this SDK's own contract version")
	s.Equal(uint32(ipc.ProtocolMajor+1), refused.Served.Major,
		"served should be the version the runtime sent")
}

func (s *HandshakeTestSuite) Test_refused_when_tags_do_not_match_the_blueprint() {
	runtime := newFakeRuntime()
	// A mismatch is a startup error rather than a 404 in production.
	runtime.accept = func(*pb.Ready) *pb.ReadyAck {
		return &pb.ReadyAck{
			Accepted:      false,
			UnknownTags:   []string{"GET::/typo"},
			UnhandledTags: []string{"GET::/orders"},
		}
	}

	client := connect(s.T(), runtime.start(s.T()), ipc.Config{
		Tags:    []string{"GET::/typo"},
		Resolve: func(string) (layer.Next, bool) { return nil, false },
	})

	err := client.Serve(testContext(s.T()))

	var mismatch *ipc.TagMismatchError
	s.Require().ErrorAs(err, &mismatch)

	// Both directions are reported: a tag the handler does not serve means
	// events dispatched nowhere, and one the blueprint does not declare means a
	// handler that can never be addressed.
	s.Equal([]string{"GET::/typo"}, mismatch.Unknown)
	s.Equal([]string{"GET::/orders"}, mismatch.Unhandled)
	s.Contains(err.Error(), "GET::/typo")
	s.Contains(err.Error(), "GET::/orders")
}

func (s *HandshakeTestSuite) Test_the_blueprints_route_key_is_declared_after_reconciliation() {
	// The runtime's configuration arrives before the handler declares itself,
	// which is the only window in which both what code registered and what the
	// blueprint declares are known.
	runtime := newFakeRuntime()
	runtime.config = &pb.RuntimeConfig{
		Handlers: []*pb.HandlerConfig{
			{HandlerName: "sendMessageHandler", HandlerTag: "event::sendMessage"},
		},
	}

	var sawBlueprintTags []string
	s.serve(runtime, ipc.Config{
		// What code built, keyed by the default rather than the API's key.
		Tags: []string{"action::sendMessage"},
		Reconcile: func(blueprint []ipc.HandlerConfig) ([]string, error) {
			for _, declared := range blueprint {
				sawBlueprintTags = append(sawBlueprintTags, declared.HandlerTag)
			}
			return sawBlueprintTags, nil
		},
		Resolve: func(string) (layer.Next, bool) { return nil, false },
	})

	ready := runtime.awaitReady(s.T())

	s.Equal([]string{"event::sendMessage"}, sawBlueprintTags,
		"reconciliation should see the blueprint's tags")
	s.Equal([]string{"event::sendMessage"}, ready.GetHandlerTags())
}

// Dispatch, and what comes back for one.
type DispatchTestSuite struct {
	suite.Suite
}

func TestDispatchTestSuite(t *testing.T) {
	suite.Run(t, new(DispatchTestSuite))
}

// dispatchHTTPOnReady returns an afterReady that sends one HTTP dispatch.
func dispatchHTTPOnReady(id, tag, path string) func(pb.HandlerRuntimeService_EventStreamServer) error {
	return func(stream pb.HandlerRuntimeService_EventStreamServer) error {
		return stream.Send(&pb.RuntimeMessage{Frame: &pb.RuntimeMessage_Dispatch{
			Dispatch: &pb.Dispatch{
				Id:         id,
				HandlerTag: tag,
				Source: &pb.Dispatch_Http{Http: &pb.HttpRequest{
					Method: "GET", Path: path, Route: path,
				}},
			},
		}})
	}
}

func (s *DispatchTestSuite) serve(runtime *fakeRuntime, config ipc.Config) {
	client := connect(s.T(), runtime.start(s.T()), config)
	go func() { _ = client.Serve(testContext(s.T())) }()
}

func (s *DispatchTestSuite) Test_a_dispatch_is_handled_and_returns_credit() {
	runtime := newFakeRuntime()
	runtime.afterReady = dispatchHTTPOnReady("event-1", "GET::/orders", "/orders")

	s.serve(runtime, ipc.Config{
		Tags: []string{"GET::/orders"},
		Resolve: func(string) (layer.Next, bool) {
			return func(_ context.Context, ev *handler.Event) (*handler.Result, error) {
				return &handler.Result{ID: ev.ID, HTTP: &handler.Response{
					Status: 200,
					Body:   []byte(`{"orders":[]}`),
				}}, nil
			}, true
		},
		Concurrency: 2,
	})

	result := runtime.awaitResult(s.T())

	s.Equal("event-1", result.GetId())
	s.Equal(uint32(200), result.GetHttp().GetStatus())
	s.Equal(uint32(1), result.GetCreditGrant())
}

func (s *DispatchTestSuite) Test_credit_is_returned_when_a_handler_panics() {
	runtime := newFakeRuntime()
	runtime.afterReady = dispatchHTTPOnReady("event-panic", "GET::/boom", "/boom")

	s.serve(runtime, ipc.Config{
		Tags: []string{"GET::/boom"},
		Resolve: func(string) (layer.Next, bool) {
			return func(context.Context, *handler.Event) (*handler.Result, error) {
				panic("handler exploded")
			}, true
		},
	})

	result := runtime.awaitResult(s.T())

	// A missed grant drains the window and stalls the stream with nothing
	// reported anywhere, so it has to survive a panic.
	s.Equal(uint32(1), result.GetCreditGrant(), "credit should survive a panic")
	s.Require().NotNil(result.GetError(), "a panic should become a handler error")
	s.Contains(result.GetError().GetMessage(), "handler exploded")
}

func (s *DispatchTestSuite) Test_an_unroutable_tag_is_reported_rather_than_dropped() {
	runtime := newFakeRuntime()
	runtime.afterReady = dispatchHTTPOnReady(
		"event-lost", "GET::/nothing-serves-this", "/nothing-serves-this")

	s.serve(runtime, ipc.Config{
		Tags:    []string{"GET::/orders"},
		Resolve: func(string) (layer.Next, bool) { return nil, false },
	})

	result := runtime.awaitResult(s.T())

	// Dropping it would spend a credit and never return it.
	s.Equal(uint32(1), result.GetCreditGrant())
	s.NotNil(result.GetError(), "an unroutable dispatch should produce an error result")
}
