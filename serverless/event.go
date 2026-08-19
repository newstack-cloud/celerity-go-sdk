package serverless

import (
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// toEvent maps a provider payload into the SDK's event vocabulary.
//
// The event carries no deadline: under a serverless platform the deadline is
// the invocation context's, which the provider's own runtime sets and which
// already reaches the handler through ctx.
func (d *dispatcher) toEvent(kind handler.Kind, payload []byte) (*handler.Event, error) {
	ev := &handler.Event{Kind: kind, Tag: d.resolved.Tag}

	var err error
	switch kind {
	case handler.KindHTTP:
		ev.HTTP, err = d.mapper.ToRequest(payload)
		if ev.HTTP != nil {
			ev.ID = ev.HTTP.RequestID
		}
	case handler.KindWebSocket:
		ev.WebSocket, err = d.mapper.ToWebSocketMessage(payload)
		if ev.WebSocket != nil {
			ev.ID = ev.WebSocket.RequestID
		}
	case handler.KindConsumer:
		ev.Consumer, err = d.mapper.ToConsumerBatch(payload, d.resolved.Tag)
	case handler.KindSchedule:
		ev.Schedule, err = d.mapper.ToScheduleTrigger(payload, d.resolved.Tag)
		if ev.Schedule != nil {
			ev.ID = ev.Schedule.MessageID
		}
	case handler.KindCustom:
		ev.Custom, err = d.mapper.ToCustomInvoke(payload)
	default:
		return nil, fmt.Errorf("unsupported event source %q", kind)
	}

	if err != nil {
		return nil, fmt.Errorf("mapping %s event: %w", kind, err)
	}
	return ev, nil
}

// fromResult maps a handler result back into what the provider's runtime
// expects to serialise.
func (d *dispatcher) fromResult(kind handler.Kind, res *handler.Result) (any, error) {
	if res == nil {
		return nil, nil
	}
	if res.Error != nil {
		return nil, res.Error
	}

	switch kind {
	case handler.KindHTTP:
		return d.mapper.FromResponse(res.HTTP)
	case handler.KindWebSocket:
		return webSocketStatus(res.WebSocket), nil
	case handler.KindConsumer:
		return d.mapper.FromBatchResult(res.Consumer)
	case handler.KindSchedule:
		return nil, scheduleError(res.Schedule)
	case handler.KindCustom:
		return d.mapper.FromCustomResult(res.Custom)
	default:
		return nil, fmt.Errorf("unsupported event source %q", kind)
	}
}

func webSocketStatus(ack *handler.Ack) map[string]int {
	if ack != nil && !ack.Success {
		return map[string]int{"statusCode": 401}
	}
	return map[string]int{"statusCode": 200}
}

// scheduleError turns a failed schedule acknowledgement into an error, so a
// failing scheduled run appears as a failed invocation rather than as a
// successful one that happened to return something.
func scheduleError(ack *handler.Ack) error {
	if ack == nil || ack.Success {
		return nil
	}
	return fmt.Errorf("scheduled handler failed: %s", ack.Error)
}
