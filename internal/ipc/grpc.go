package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
)

// grpcTransport is the stream the runtime serves, adapted to [Transport].
//
// The generated client gives a fully typed bidirectional stream, so this is a
// conversion layer and nothing more.
type grpcTransport struct {
	conn   *grpc.ClientConn
	stream grpc.BidiStreamingClient[pb.HandlerMessage, pb.RuntimeMessage]
}

func (t *grpcTransport) Send(frame *ToRuntime) error {
	msg := toHandlerMessage(frame)
	if msg == nil {
		return errors.New("celerity: refusing to send an empty frame")
	}
	return t.stream.Send(msg)
}

func (t *grpcTransport) Recv() (*FromRuntime, error) {
	msg, err := t.stream.Recv()
	if err != nil {
		return nil, err
	}
	return fromRuntimeMessage(msg), nil
}

func (t *grpcTransport) Close() error {
	// The send side is closed first so the runtime sees the stream end rather
	// than a connection that stopped answering.
	if err := t.stream.CloseSend(); err != nil {
		_ = t.conn.Close()
		return err
	}
	return t.conn.Close()
}

// Dial connects to the runtime's handler stream and opens the event stream.
//
// Dialling is retried: a handler process is commonly started alongside the
// runtime by a supervisor, so losing the race to the runtime's listener is
// ordinary rather than fatal.
func Dial(ctx context.Context, config DialConfig) (Transport, error) {
	target, err := config.Target()
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialerFor(target)),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to the runtime at %s: %w", target, err)
	}

	stream, err := openStream(ctx, conn, config.RetryFor)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &grpcTransport{conn: conn, stream: stream}, nil
}

// dialerFor returns nil for a TCP target, letting gRPC use its own dialler, and
// a unix dialler otherwise.
func dialerFor(target string) func(context.Context, string) (net.Conn, error) {
	path, ok := strings.CutPrefix(target, "unix://")
	if !ok {
		return nil
	}
	return func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}

func openStream(
	ctx context.Context,
	conn *grpc.ClientConn,
	retryFor time.Duration,
) (grpc.BidiStreamingClient[pb.HandlerMessage, pb.RuntimeMessage], error) {
	client := pb.NewHandlerRuntimeServiceClient(conn)
	deadline := time.Now().Add(retryFor)
	backoff := 25 * time.Millisecond

	for {
		stream, err := client.EventStream(ctx)
		if err == nil {
			return stream, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("opening the handler event stream: %w", err)
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if backoff < time.Second {
			backoff *= 2
		}
	}
}
