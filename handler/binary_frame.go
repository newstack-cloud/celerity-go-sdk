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
	// ErrRouteReserved is returned when a route begins with a byte the protocol
	// keeps for its own messages.
	ErrRouteReserved = errors.New(
		"celerity: a binary message route must not begin with a reserved byte",
	)
	// ErrAckWithoutID is returned when a message asks to be acknowledged without
	// an id for the acknowledgement to name.
	ErrAckWithoutID = errors.New(
		"celerity: a binary message asking to be acknowledged needs an id",
	)
)

// reservedRouteBytes are the first bytes a client reads as one of the
// protocol's own messages: ping, pong, a lost message, an acknowledgement and
// the capabilities signal.
var reservedRouteBytes = [...]bool{0x1: true, 0x2: true, 0x3: true, 0x4: true, 0x5: true}

// FrameBinaryMessage encodes a message in the Celerity Binary Message Format.
//
// [BinarySender.SendBinary] calls this, and is how an application sends binary;
// this is exported for a caller composing a frame for a transport of its own.
//
// Unlike the Node and Python SDKs, which hand framing to the runtime through
// their native bindings, this is the Go implementation of the format: the Go
// SDK reaches the runtime over IPC and has no in-process binding to call. It is
// therefore a second implementation of a published wire format, and is held to
// the same bytes by the shared conformance vectors in testdata.
//
// Every field that cannot be represented is refused rather than truncated into
// a frame that would be read as something other than what was meant, which is
// what the runtime does with the same message.
func FrameBinaryMessage(route, messageID string, requireAck bool, message []byte) ([]byte, error) {
	if len(route) == 0 {
		return nil, ErrRouteRequired
	}
	if len(route) > 255 {
		return nil, ErrRouteTooLong
	}
	// A route is read as reserved when its first byte is one of the reserved
	// values, so a custom route starting with one would come back as a ping or
	// an acknowledgement rather than as itself.
	if first := route[0]; int(first) < len(reservedRouteBytes) && reservedRouteBytes[first] {
		return nil, ErrRouteReserved
	}
	if len(messageID) > 255 {
		return nil, ErrMessageIDTooLong
	}
	// Asking to be acknowledged without an id is refused rather than quietly
	// dropped, since the sender would otherwise wait for an answer that has
	// nothing to name and can never come.

	if requireAck && messageID == "" {
		return nil, ErrAckWithoutID
	}

	framed := make([]byte, 0, 3+len(route)+len(messageID)+len(message))
	framed = append(framed, byte(len(route)))
	framed = append(framed, route...)

	ack := byte(0x0)
	if requireAck {
		ack = 0x1
	}
	framed = append(framed, ack, byte(len(messageID)))
	framed = append(framed, messageID...)
	return append(framed, message...), nil
}
