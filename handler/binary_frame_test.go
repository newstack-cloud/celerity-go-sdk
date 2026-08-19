package handler_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// The frame layout is <routeLength><route><requireAck><messageIdLength><messageId><message>,
// every length one byte. The runtime refuses a binary message it cannot read as
// a frame, so these cases check the bytes rather than a round trip.

func TestFrameBinaryMessage(t *testing.T) {
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
			name:       "ack is ignored without an id to acknowledge",
			route:      "ping",
			requireAck: true,
			want:       []byte{4, 'p', 'i', 'n', 'g', 0x0, 0x0},
		},
		{
			name:      "an empty payload is allowed",
			route:     "ping",
			messageID: "m1",
			want:      []byte{4, 'p', 'i', 'n', 'g', 0x0, 2, 'm', '1'},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := handler.FrameBinaryMessage(tc.route, tc.messageID, tc.requireAck, tc.message)
			if err != nil {
				t.Fatalf("FrameBinaryMessage returned %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("FrameBinaryMessage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFrameBinaryMessageRejectsWhatTheRuntimeWould(t *testing.T) {
	longValue := string(bytes.Repeat([]byte("a"), 256))

	cases := []struct {
		name      string
		route     string
		messageID string
		want      error
	}{
		{"no route", "", "m1", handler.ErrRouteRequired},
		{"route longer than its length byte", longValue, "m1", handler.ErrRouteTooLong},
		{"id longer than its length byte", "chat", longValue, handler.ErrMessageIDTooLong},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handler.FrameBinaryMessage(tc.route, tc.messageID, false, nil)
			if !errors.Is(err, tc.want) {
				t.Errorf("FrameBinaryMessage error = %v, want %v", err, tc.want)
			}
		})
	}
}
