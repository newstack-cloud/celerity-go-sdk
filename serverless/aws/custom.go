package aws

import (
	"encoding/json"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// ToCustomInvoke maps a direct invocation payload into a custom invoke.
//
// The payload is the caller's own and is carried through untouched. The handler
// name comes from what the deployment set, since a direct invocation names the
// function rather than a handler within it.
//
// An envelope of {"handlerName": ..., "payload": ...} is honoured where the
// deployment did not name anything, which is how one function serving several custom
// handlers is addressed. A payload that is not an envelope is the input, so an
// application whose own input happens to carry a handlerName field is only
// read that way when nothing else said which handler this is.
func (m Mapper) ToCustomInvoke(payload []byte) (*handler.CustomInvoke, error) {
	if m.handlerID != "" {
		return &handler.CustomInvoke{
			HandlerName: m.handlerID,
			Input:       payload,
		}, nil
	}

	var envelope struct {
		HandlerName string          `json:"handlerName"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil && envelope.HandlerName != "" {
		input := payload
		if envelope.Payload != nil {
			input = envelope.Payload
		}
		return &handler.CustomInvoke{
			HandlerName: envelope.HandlerName,
			Input:       input,
		}, nil
	}

	return &handler.CustomInvoke{
		Input: payload,
	}, nil
}

// FromCustomResult returns the handler's output as the invocation's own.
//
// Output is opaque to the SDK, and Lambda serialises what a function returns,
// so JSON output is passed through as itself rather than re-encoded into a
// quoted string. Output that is not JSON is returned as a string, which is what
// a caller reading the response can make sense of.
func (m Mapper) FromCustomResult(res *handler.CustomInvokeResult) (any, error) {
	if res == nil || len(res.Output) == 0 {
		return nil, nil
	}
	if json.Valid(res.Output) {
		return json.RawMessage(res.Output), nil
	}
	return string(res.Output), nil
}
