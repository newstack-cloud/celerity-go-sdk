package handler_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// The frame layout is <routeLength><route><requireAck><messageIdLength><messageId><message>,
// every length one byte. The runtime refuses a binary message it cannot read as
// a frame, so these cases check the bytes rather than a round trip.
type BinaryFrameTestSuite struct {
	suite.Suite
}

func TestBinaryFrameTestSuite(t *testing.T) {
	suite.Run(t, new(BinaryFrameTestSuite))
}

func (s *BinaryFrameTestSuite) Test_frames_are_laid_out_as_the_format_specifies() {
	cases := []struct {
		name       string
		route      string
		messageID  string
		requireAck bool
		message    []byte
		want       []byte
	}{
		{
			name:      "route, id and payload",
			route:     "chat",
			messageID: "m1",
			message:   []byte("hi"),
			want:      []byte{4, 'c', 'h', 'a', 't', 0x0, 2, 'm', '1', 'h', 'i'},
		},
		{
			name:       "ack requested",
			route:      "chat",
			messageID:  "m1",
			requireAck: true,
			message:    []byte("hi"),
			want:       []byte{4, 'c', 'h', 'a', 't', 0x1, 2, 'm', '1', 'h', 'i'},
		},
		{
			name:    "no id sets both the ack flag and the id length to zero",
			route:   "chat",
			message: []byte("hi"),
			want:    []byte{4, 'c', 'h', 'a', 't', 0x0, 0x0, 'h', 'i'},
		},
		{
			name:      "an empty payload is allowed",
			route:     "ping",
			messageID: "m1",
			want:      []byte{4, 'p', 'i', 'n', 'g', 0x0, 2, 'm', '1'},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			got, err := handler.FrameBinaryMessage(tc.route, tc.messageID, tc.requireAck, tc.message)

			s.Require().NoError(err)
			s.Equal(tc.want, got)
		})
	}
}

func (s *BinaryFrameTestSuite) Test_framing_refuses_what_the_runtime_would() {
	longValue := string(bytes.Repeat([]byte("a"), 256))

	cases := []struct {
		name       string
		route      string
		messageID  string
		requireAck bool
		want       error
	}{
		{"no route", "", "m1", false, handler.ErrRouteRequired},
		{"route longer than its length byte", longValue, "m1", false, handler.ErrRouteTooLong},
		{"id longer than its length byte", "chat", longValue, false, handler.ErrMessageIDTooLong},
		// A client reads a route beginning with a reserved byte as one of the
		// protocol's own messages rather than as the route it was meant to be.
		{"route beginning with a reserved byte", "\x04ck", "m1", false, handler.ErrRouteReserved},
		// Sending it anyway would leave the sender waiting for an answer that
		// has nothing to name, so it is refused rather than quietly dropped.
		{"asking to be acknowledged with no id", "chat", "", true, handler.ErrAckWithoutID},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			_, err := handler.FrameBinaryMessage(tc.route, tc.messageID, tc.requireAck, nil)

			s.ErrorIs(err, tc.want)
		})
	}
}
