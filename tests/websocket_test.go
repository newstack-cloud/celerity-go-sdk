//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/suite"
)

// WebSocket messages routed by the real runtime. What a stand-in cannot cover
// here is the routing itself as the route key is the API's, the route is the
// handler's, and the tag the runtime builds from them has to be the one the SDK
// declared or the handshake would have failed before any of this ran.
type RuntimeWebSocketTestSuite struct {
	suite.Suite
}

func TestRuntimeWebSocketTestSuite(t *testing.T) {
	suite.Run(t, new(RuntimeWebSocketTestSuite))
}

// The API is hybrid, so each protocol is served under its own base path and
// the blueprint puts WebSocket at /ws.
func websocketURL() string {
	url := os.Getenv("CELERITY_TEST_RUNTIME_URL")
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	return strings.Replace(url, "http", "ws", 1) + "/ws"
}

func (s *RuntimeWebSocketTestSuite) dial() (*websocket.Conn, context.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.T().Cleanup(cancel)

	conn, res, err := websocket.Dial(ctx, websocketURL(), nil)
	if err != nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		s.Require().NoError(err, "dialling the runtime's websocket (status %d)", status)
	}
	s.T().Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })

	return conn, ctx
}

// readTextMessage returns the next text message, skipping the protocol's own
// binary frames.
//
// The runtime sends reserved binary frames of its own, a capabilities signal on
// connect among them, and a client that treats the first frame it sees as the
// application's reply reads one of those instead. Application messages sent as
// text are the ones this suite is for.
func (s *RuntimeWebSocketTestSuite) readTextMessage(
	ctx context.Context,
	conn *websocket.Conn,
) map[string]string {
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	for {
		kind, received, err := conn.Read(readCtx)
		s.Require().NoError(err, "reading the handler's reply")

		if kind != websocket.MessageText {
			continue
		}

		var message map[string]string
		s.Require().NoError(json.Unmarshal(received, &message), "decoding %s", received)
		return message
	}
}

// Sends one routed message and returns what the handler pushed back.
func (s *RuntimeWebSocketTestSuite) sendMessage(
	ctx context.Context,
	conn *websocket.Conn,
	text string,
) map[string]string {
	// The API states no route key, so it resolves to the runtime's default and
	// this is the field the route travels in.
	body, err := json.Marshal(map[string]string{"event": "sendMessage", "text": text})
	s.Require().NoError(err, "encoding")
	s.Require().NoError(conn.Write(ctx, websocket.MessageText, body), "writing to the runtime")

	return s.readTextMessage(ctx, conn)
}

func (s *RuntimeWebSocketTestSuite) Test_a_message_is_routed_to_its_handler() {
	conn, ctx := s.dial()

	// The handler answers on the side channel rather than by returning, so this
	// exercises WsSend and its acknowledgement against the real runtime.
	reply := s.sendMessage(ctx, conn, "hello from the client")

	s.Equal("hello from the client", reply["echo"],
		"the message should come back over the side channel")
}

func (s *RuntimeWebSocketTestSuite) Test_connect_and_disconnect_serve_every_connection() {
	// $connect and $disconnect are declared by the blueprint, so a handler
	// process that did not register them would have failed the handshake and
	// nothing here would run. What this adds is that they serve connection after
	// connection rather than only the first.
	first, ctx := s.dial()
	s.Require().Equal("one", s.sendMessage(ctx, first, "one")["echo"])
	s.Require().NoError(first.Close(websocket.StatusNormalClosure, "done"),
		"closing, which runs $disconnect")

	second, secondCtx := s.dial()
	s.Equal("two", s.sendMessage(secondCtx, second, "two")["echo"],
		"the socket should be usable after a disconnect")

	// The runtime is still serving HTTP, so nothing faulted on the way through.
	res, err := http.Get(runtimeURL() + "/orders/after-disconnect")
	s.Require().NoError(err, "requesting after the disconnect")
	defer res.Body.Close()

	s.Equal(http.StatusOK, res.StatusCode, "the runtime should still be serving")
}

func (s *RuntimeWebSocketTestSuite) Test_concurrent_connections_each_get_their_own_reply() {
	// The side channel names the connection to answer on, and every handler
	// shares one stream, so a reply reaching the wrong socket is the failure
	// worth ruling out.
	a, ctxA := s.dial()
	b, ctxB := s.dial()

	s.Equal("for-a", s.sendMessage(ctxA, a, "for-a")["echo"])
	s.Equal("for-b", s.sendMessage(ctxB, b, "for-b")["echo"])
}
