package aws

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// HandlerKindEnvVar names the source a function serves, where the deployment
// knows it.
//
// Consulted before the payload is looked at. A Celerity deployment sets it, and
// it is the only thing that can distinguish a custom invocation from a payload
// that happens to resemble another source, since a custom payload is the
// caller's own JSON and can hold anything.
const HandlerKindEnvVar = "CELERITY_HANDLER_TYPE"

// A minimal view of the shapes worth telling apart, so that detection reads
// only the keys it decides on and never the payload's own body.
type envelope struct {
	RequestContext *struct {
		HTTP         *json.RawMessage `json:"http"`
		ConnectionID *string          `json:"connectionId"`
		EventType    *string          `json:"eventType"`
	} `json:"requestContext"`
	Records []struct {
		EventSource string `json:"eventSource"`
	} `json:"Records"`
	Source     *string `json:"source"`
	DetailType *string `json:"detail-type"`
}

// Detect reports which source a payload came from.
//
// A payload matching nothing is a custom invocation, it is the source with no
// shape of its own, being whatever the caller sent.
func (m Mapper) Detect(payload []byte) (handler.Kind, error) {
	if m.handlerKind != "" {
		return m.handlerKind, nil
	}

	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		// Not JSON at all, which an API Gateway or SQS event always is. A custom
		// invocation is opaque, so this is one.
		return handler.KindCustom, nil
	}

	if rc := env.RequestContext; rc != nil {
		switch {
		case rc.HTTP != nil:
			return handler.KindHTTP, nil
		case rc.ConnectionID != nil && rc.EventType != nil:
			return handler.KindWebSocket, nil
		}
	}
	if len(env.Records) > 0 && env.Records[0].EventSource == sqsEventSource {
		return handler.KindConsumer, nil
	}

	if env.Source != nil && env.DetailType != nil {
		return handler.KindSchedule, nil
	}

	return handler.KindCustom, nil
}

func kindFromEnv() (handler.Kind, bool) {
	kind := handler.Kind(os.Getenv(HandlerKindEnvVar))

	switch kind {
	case handler.KindHTTP, handler.KindWebSocket, handler.KindConsumer,
		handler.KindSchedule, handler.KindCustom:
		return kind, true
	default:
		return "", false
	}
}

// Reads a payload into the AWS event shape it is expected to be.
//
// A payload that does not fit is an error rather than an empty event as it means
// the function is wired to a source it was not built for, and a handler given
// a zero-valued request would answer as though the request had simply been
// empty.
func decode[T any](payload []byte, source string) (*T, error) {
	var event T
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, fmt.Errorf("reading a %s event: %w", source, err)
	}
	return &event, nil
}
