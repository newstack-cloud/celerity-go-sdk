package ipc_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	pb "github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
)

// A stand-in for the Celerity runtime, serving the real contract over a real
// unix socket.
//
// The generated server stubs come from the same .proto the runtime is built
// from, so the bytes on this socket are the bytes on a real one. That is what
// lets the protocol be exercised without the runtime's container image, which
// matters because the image hosting ahead-of-time compiled handlers is not
// released yet, and because a test that needs Docker is a test that does not
// run while someone is working.
//
// What it deliberately does not cover is the runtime's own behaviour: routing,
// auth, CORS, blueprint parsing. None of that is the SDK's to get right.
type fakeRuntime struct {
	pb.UnimplementedHandlerRuntimeServiceServer

	// config is sent before the handler declares itself, as the runtime does.
	config *pb.RuntimeConfig
	// accept decides the handshake, letting a test drive a tag mismatch.
	accept func(ready *pb.Ready) *pb.ReadyAck
	// afterReady runs once the handshake is done, and is where a test sends
	// dispatches. The stream stays open after it returns.
	afterReady func(stream pb.HandlerRuntimeService_EventStreamServer) error

	ready chan *pb.Ready
	sent  chan *pb.HandlerMessage
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		config: &pb.RuntimeConfig{},
		accept: func(*pb.Ready) *pb.ReadyAck { return &pb.ReadyAck{Accepted: true} },
		ready:  make(chan *pb.Ready, 1),
		sent:   make(chan *pb.HandlerMessage, 16),
	}
}

func (f *fakeRuntime) EventStream(stream pb.HandlerRuntimeService_EventStreamServer) error {
	if err := stream.Send(&pb.RuntimeMessage{
		Frame: &pb.RuntimeMessage_Config{Config: f.config},
	}); err != nil {
		return err
	}

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	ready := first.GetReady()
	if ready == nil {
		return errors.New("handler sent a frame before declaring itself")
	}
	f.ready <- ready

	if err := stream.Send(&pb.RuntimeMessage{
		Frame: &pb.RuntimeMessage_ReadyAck{ReadyAck: f.accept(ready)},
	}); err != nil {
		return err
	}

	// Everything the handler sends from here is recorded, so a test can assert
	// on results and credit without racing the frame loop.
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				return
			}
			f.sent <- msg
		}
	}()

	if f.afterReady != nil {
		if err := f.afterReady(stream); err != nil {
			return err
		}
	}

	// The stream stays open until the client closes it. Returning here would
	// end it, and a handler mid-dispatch would fail its send rather than
	// deliver a result.
	<-stream.Context().Done()
	return nil
}

// start serves the fake on a unix socket and returns its path.
func (f *fakeRuntime) start(t *testing.T) string {
	t.Helper()

	// A unix socket path is limited to around 104 bytes on macOS, which
	// t.TempDir's name-derived path can exceed.
	dir, err := os.MkdirTemp("", "celipc")
	if err != nil {
		t.Fatalf("creating a socket directory: %v", err)
	}
	socket := filepath.Join(dir, "runtime.sock")

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", socket, err)
	}

	server := grpc.NewServer()
	pb.RegisterHandlerRuntimeServiceServer(server, f)
	go func() { _ = server.Serve(listener) }()

	t.Cleanup(func() {
		server.Stop()
		_ = os.RemoveAll(dir)
	})
	return socket
}

// awaitReady returns the handshake the handler sent.
func (f *fakeRuntime) awaitReady(t *testing.T) *pb.Ready {
	t.Helper()
	select {
	case ready := <-f.ready:
		return ready
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never declared itself")
		return nil
	}
}

// awaitResult returns the next result the handler sent.
func (f *fakeRuntime) awaitResult(t *testing.T) *pb.Result {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-f.sent:
			if result := msg.GetResult(); result != nil {
				return result
			}
		case <-deadline:
			t.Fatal("the handler never sent a result")
			return nil
		}
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}
