package aws

import (
	"encoding/json"

	"github.com/aws/aws-lambda-go/events"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// ToWebSocketMessage maps an API Gateway WebSocket v2 event into a WebSocket
// message.
func (m Mapper) ToWebSocketMessage(payload []byte) (*handler.WebSocketMessage, error) {
	event, err := decode[events.APIGatewayWebsocketProxyRequest](payload, "API Gateway WebSocket")
	if err != nil {
		return nil, err
	}

	rc := event.RequestContext
	return &handler.WebSocketMessage{
		// The route key API Gateway matched is the route, for all three cases
		// it can be one of $connect, $disconnect, or the value the route selection
		// expression read out of the message. Those are the same names a
		// handler registers against, because the API's route key and the
		// runtime's are the same field.
		Route:        rc.RouteKey,
		ConnectionID: rc.ConnectionID,
		SourceIP:     rc.Identity.SourceIP,
		RequestID:    rc.RequestID,
		Message:      []byte(event.Body),
		// API Gateway WebSocket APIs carry text frames only, and disconnect a
		// client that sends a binary one, so nothing arriving here is binary.
		IsBinary: false,
		// The id the client gave the message where it gave one, so that an
		// acknowledgement and a loss notification name what the client knows it
		// by. The request id is the delivery's own and is carried separately.
		MessageID: firstNonEmpty(clientMessageID(event.Body), rc.RequestID),
	}, nil
}

// ManagementEndpoint returns the API Gateway Management API endpoint to push
// back to for the connection an event came from.
//
// The event carries it, which is what lets one deployment serve several stages
// and domains without the endpoint being configured anywhere.
func ManagementEndpoint(payload []byte) (string, error) {
	event, err := decode[events.APIGatewayWebsocketProxyRequest](payload, "API Gateway WebSocket")
	if err != nil {
		return "", err
	}
	rc := event.RequestContext
	return "https://" + rc.DomainName + "/" + rc.Stage, nil
}

// The part of a message body the WebSocket Runtime Protocol
// defines, which is read while the rest of the body stays the application's.
type clientRequest struct {
	Ack       bool   `json:"ack"`
	MessageID string `json:"messageId"`
}

func parseClientRequest(body string) (clientRequest, bool) {
	if body == "" {
		return clientRequest{}, false
	}
	var req clientRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		// An application is free to send anything the API's route selection
		// expression can read, so a body that is not a JSON object carries no
		// protocol fields rather than being an error.
		return clientRequest{}, false
	}
	return req, true
}

func clientMessageID(body string) string {
	req, _ := parseClientRequest(body)
	return req.MessageID
}

// Returns the id of the message the client asked to have
// acknowledged, if it asked.
//
// Both halves are required as an acknowledgement names a message, so a request
// carrying no id asks for one that could not be sent.
func clientAckRequest(body string) (string, bool) {
	req, ok := parseClientRequest(body)
	if !ok || !req.Ack || req.MessageID == "" {
		return "", false
	}
	return req.MessageID, true
}

// Returns the acknowledgement to send back, as JSON text.
//
// API Gateway WebSocket APIs carry text frames only, so the protocol's binary
// control frames physically cannot reach a client here. Their absence is what
// tells the client it is in a constrained environment, where the protocol
// specifies acknowledgements as JSON text instead.
//
// The event key is the protocol's own and holds the reserved value "ack",
// rather than following the API's configured route key, an acknowledgement is a
// control message the client recognises, not one the server routes.
func composeClientAck(messageID string, epochSeconds int64) ([]byte, error) {
	return json.Marshal(map[string]any{
		"event": "ack",
		"data": map[string]string{
			"messageId": messageID,
			"timestamp": itoa(epochSeconds),
		},
	})
}
