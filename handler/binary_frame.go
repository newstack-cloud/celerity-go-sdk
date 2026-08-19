package handler

import "errors"

// The Celerity Binary Message Format:
//
//	<routeLength><route><requireAck><messageIdLength><messageId><message>
//
// Every length is a single byte, so a route and a message id are each at most
// 255 bytes. A client reads every binary frame that is not a reserved one as a
// framed message, so an unframed payload is not read as data, it is read as a
// route length and a route, leaving the application a short payload under a
// route nothing serves. The runtime refuses a binary message it cannot read as
// a frame rather than sending one no client can use.

var (
	// ErrRouteRequired is returned when framing a binary message with no route.
	ErrRouteRequired = errors.New("celerity: a binary message must name the route it is for")
	// ErrRouteTooLong is returned when a route exceeds the single byte the
	// format gives its length.
	ErrRouteTooLong = errors.New("celerity: a binary message route must be at most 255 bytes")
	// ErrMessageIDTooLong is returned when a message id exceeds the single byte
	// the format gives its length.
	ErrMessageIDTooLong = errors.New("celerity: a binary message id must be at most 255 bytes")
)

// FrameBinaryMessage encodes a message in the Celerity Binary Message Format,
// ready to be sent through a [WebSocketSender] with IsBinary set.
//
// A message carrying no id sets both requireAck and messageIdLength to zero, so
// requireAck is ignored when messageID is empty.
func FrameBinaryMessage(route, messageID string, requireAck bool, message []byte) ([]byte, error) {
	if len(route) == 0 {
		return nil, ErrRouteRequired
	}
	if len(route) > 255 {
		return nil, ErrRouteTooLong
	}
	if len(messageID) > 255 {
		return nil, ErrMessageIDTooLong
	}

	framed := make([]byte, 0, 3+len(route)+len(messageID)+len(message))
	framed = append(framed, byte(len(route)))
	framed = append(framed, route...)

	ack := byte(0x0)
	if requireAck && messageID != "" {
		ack = 0x1
	}
	framed = append(framed, ack, byte(len(messageID)))
	framed = append(framed, messageID...)
	return append(framed, message...), nil
}
