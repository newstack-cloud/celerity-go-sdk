package ipc

import (
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	pb "github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
)

// Conversion between the wire contract and the SDK's own frame types.
//
// The frame loop works in the SDK's types so that it can be exercised without a
// transport, and this file is the only place that knows about the generated
// ones.

func fromRuntimeMessage(msg *pb.RuntimeMessage) *FromRuntime {
	switch frame := msg.GetFrame().(type) {
	case *pb.RuntimeMessage_Config:
		return &FromRuntime{Config: runtimeConfigFrom(frame.Config)}
	case *pb.RuntimeMessage_ReadyAck:
		return &FromRuntime{ReadyAck: &ReadyAck{
			Accepted:      frame.ReadyAck.GetAccepted(),
			UnknownTags:   frame.ReadyAck.GetUnknownTags(),
			UnhandledTags: frame.ReadyAck.GetUnhandledTags(),
			Reason:        RefusedReason(frame.ReadyAck.GetRefusedReason()),
		}}
	case *pb.RuntimeMessage_Dispatch:
		return &FromRuntime{Dispatch: eventFrom(frame.Dispatch)}
	case *pb.RuntimeMessage_Cancel:
		return &FromRuntime{Cancel: &Cancel{
			ID:     frame.Cancel.GetId(),
			Reason: CancelReason(frame.Cancel.GetReason()),
		}}
	case *pb.RuntimeMessage_Drain:
		return &FromRuntime{Drain: &Drain{
			Deadline: unixMillis(frame.Drain.GetDeadlineUnixMs()),
		}}
	case *pb.RuntimeMessage_WsAck:
		return &FromRuntime{WsAck: wsAckFrom(frame.WsAck)}
	default:
		// A frame this SDK does not know is ignored rather than fatal: the
		// contract adds frames compatibly, and refusing one would turn a
		// backwards-compatible runtime upgrade into a startup failure.
		return &FromRuntime{}
	}
}

func runtimeConfigFrom(c *pb.RuntimeConfig) *RuntimeConfig {
	handlers := make([]HandlerConfig, 0, len(c.GetHandlers()))
	for _, h := range c.GetHandlers() {
		handlers = append(handlers, HandlerConfig{
			HandlerName:    h.GetHandlerName(),
			HandlerTag:     h.GetHandlerTag(),
			Timeout:        time.Duration(h.GetTimeoutMs()) * time.Millisecond,
			TracingEnabled: h.GetTracingEnabled(),
			PublishedName:  h.GetPublishedName(),
		})
	}
	return &RuntimeConfig{
		TracingEnabled: c.GetTracingEnabled(),
		MetricsEnabled: c.GetMetricsEnabled(),
		Handlers:       handlers,
		Protocol: ProtocolVersion{
			Major: c.GetProtocolVersion().GetMajor(),
			Minor: c.GetProtocolVersion().GetMinor(),
		},
	}
}

func eventFrom(d *pb.Dispatch) *handler.Event {
	ev := &handler.Event{
		ID:           d.GetId(),
		Tag:          d.GetHandlerTag(),
		Timestamp:    unixMillis(int64(d.GetTimestampMs())),
		Deadline:     unixMillis(d.GetDeadlineUnixMs()),
		TraceContext: d.GetTraceContext(),
	}

	switch source := d.GetSource().(type) {
	case *pb.Dispatch_Http:
		ev.Kind = handler.KindHTTP
		ev.HTTP = requestFrom(source.Http)
	case *pb.Dispatch_Websocket:
		ev.Kind = handler.KindWebSocket
		ev.WebSocket = webSocketMessageFrom(source.Websocket)
	case *pb.Dispatch_Consumer:
		ev.Kind = handler.KindConsumer
		ev.Consumer = consumerBatchFrom(source.Consumer)
	case *pb.Dispatch_Schedule:
		ev.Kind = handler.KindSchedule
		ev.Schedule = &handler.ScheduleTrigger{
			ScheduleID: source.Schedule.GetScheduleId(),
			MessageID:  source.Schedule.GetMessageId(),
			Schedule:   source.Schedule.GetSchedule(),
			Input:      source.Schedule.GetInput(),
			Vendor:     source.Schedule.GetVendor(),
		}
	case *pb.Dispatch_Custom:
		ev.Kind = handler.KindCustom
		ev.Custom = &handler.CustomInvoke{
			HandlerName: source.Custom.GetHandlerName(),
			Input:       source.Custom.GetInput(),
		}
	}

	return ev
}

func requestFrom(r *pb.HttpRequest) *handler.Request {
	return &handler.Request{
		Method:      r.GetMethod(),
		Path:        r.GetPath(),
		Route:       r.GetRoute(),
		PathParams:  paramsFrom(r.GetPathParams()),
		QueryParams: paramsFrom(r.GetQueryParams()),
		Headers:     paramsFrom(r.GetHeaders()),
		SourceIP:    r.GetSourceIp(),
		RequestID:   r.GetRequestId(),
		Body:        r.GetBody(),
	}
}

func webSocketMessageFrom(m *pb.WebSocketMessage) *handler.WebSocketMessage {
	return &handler.WebSocketMessage{
		Route:        m.GetRoute(),
		ConnectionID: m.GetConnectionId(),
		SourceIP:     m.GetSourceIp(),
		RequestID:    m.GetRequestId(),
		Message:      m.GetMessage(),
		IsBinary:     m.GetIsBinary(),
		MessageID:    m.GetMessageId(),
	}
}

func consumerBatchFrom(b *pb.ConsumerBatch) *handler.ConsumerBatch {
	records := make([]handler.ConsumerRecord, 0, len(b.GetRecords()))
	for _, r := range b.GetRecords() {
		records = append(records, handler.ConsumerRecord{
			MessageID:  r.GetMessageId(),
			Body:       r.GetBody(),
			Source:     r.GetSource(),
			EventType:  r.GetEventType(),
			Attributes: r.GetAttributes(),
			Vendor:     r.GetVendor(),
		})
	}
	return &handler.ConsumerBatch{
		Records:    records,
		SourceID:   b.GetSourceId(),
		SourceType: b.GetSourceType(),
		Vendor:     b.GetVendor(),
	}
}

func wsAckFrom(a *pb.WsSendAck) *WsSendAck {
	failures := make([]handler.SendFailure, 0, len(a.GetFailures()))
	for _, f := range a.GetFailures() {
		failures = append(failures, handler.SendFailure{
			Index:        int(f.GetIndex()),
			ConnectionID: f.GetConnectionId(),
			Error:        f.GetErrorMessage(),
		})
	}
	return &WsSendAck{
		CorrelationID: a.GetCorrelationId(),
		Success:       a.GetSuccess(),
		Failures:      failures,
	}
}

func toHandlerMessage(frame *ToRuntime) *pb.HandlerMessage {
	switch {
	case frame.Ready != nil:
		return &pb.HandlerMessage{Frame: &pb.HandlerMessage_Ready{Ready: readyTo(frame.Ready)}}
	case frame.Result != nil:
		return &pb.HandlerMessage{Frame: &pb.HandlerMessage_Result{Result: resultTo(frame.Result)}}
	case frame.Credit != nil:
		return &pb.HandlerMessage{Frame: &pb.HandlerMessage_Credit{
			Credit: &pb.CreditGrant{Additional: frame.Credit.Additional},
		}}
	case frame.WsSend != nil:
		return &pb.HandlerMessage{Frame: &pb.HandlerMessage_WsSend{WsSend: wsSendTo(frame.WsSend)}}
	case frame.Draining != nil:
		return &pb.HandlerMessage{Frame: &pb.HandlerMessage_Draining{
			Draining: &pb.Draining{DeadlineUnixMs: frame.Draining.Deadline.UnixMilli()},
		}}
	default:
		return nil
	}
}

func readyTo(r *Ready) *pb.Ready {
	limits := make([]*pb.HandlerLimit, 0, len(r.Limits))
	for _, l := range r.Limits {
		limits = append(limits, &pb.HandlerLimit{
			HandlerTag:    l.HandlerTag,
			MaxConcurrent: l.MaxConcurrent,
		})
	}
	return &pb.Ready{
		HandlerTags:   r.HandlerTags,
		InitialCredit: r.InitialCredit,
		SdkVersion:    r.SDKVersion,
		Limits:        limits,
		ProtocolVersion: &pb.ProtocolVersion{
			Major: r.Protocol.Major,
			Minor: r.Protocol.Minor,
		},
	}
}

// resultTo carries the credit grant on every result, including one produced
// from a panic. A result that grants nothing shrinks the credit window
// permanently, and enough of them stall the stream with nothing reported.
func resultTo(r *Result) *pb.Result {
	out := &pb.Result{
		Id:          r.Outcome.ID,
		CreditGrant: r.CreditGrant,
	}

	switch {
	case r.Outcome.Error != nil:
		out.Outcome = &pb.Result_Error{Error: &pb.HandlerError{
			Message: r.Outcome.Error.Message,
			Type:    r.Outcome.Error.Type,
			Stack:   r.Outcome.Error.Stack,
		}}
	case r.Outcome.HTTP != nil:
		out.Outcome = &pb.Result_Http{Http: &pb.HttpResponse{
			Status:  uint32(r.Outcome.HTTP.Status),
			Headers: paramsTo(r.Outcome.HTTP.Headers),
			Body:    r.Outcome.HTTP.Body,
		}}
	case r.Outcome.WebSocket != nil:
		out.Outcome = &pb.Result_Websocket{Websocket: ackTo(r.Outcome.WebSocket)}
	case r.Outcome.Consumer != nil:
		out.Outcome = &pb.Result_Consumer{Consumer: batchResultTo(r.Outcome.Consumer)}
	case r.Outcome.Schedule != nil:
		out.Outcome = &pb.Result_Schedule{Schedule: ackTo(r.Outcome.Schedule)}
	case r.Outcome.Custom != nil:
		out.Outcome = &pb.Result_Custom{Custom: &pb.CustomInvokeResult{
			Output: r.Outcome.Custom.Output,
		}}
	}

	return out
}

func ackTo(a *handler.Ack) *pb.Ack {
	return &pb.Ack{Success: a.Success, ErrorMessage: a.Error}
}

func batchResultTo(b *handler.BatchResult) *pb.BatchResult {
	failures := make([]*pb.RecordFailure, 0, len(b.Failures))
	for _, f := range b.Failures {
		failures = append(failures, &pb.RecordFailure{
			MessageId:    f.MessageID,
			ErrorMessage: f.Error,
		})
	}
	return &pb.BatchResult{Success: !b.Failed(), Failures: failures}
}

func wsSendTo(s *WsSend) *pb.WsSend {
	messages := make([]*pb.WsOutbound, 0, len(s.Messages))
	for _, m := range s.Messages {
		messages = append(messages, &pb.WsOutbound{
			ConnectionId:        m.ConnectionID,
			Message:             m.Message,
			IsBinary:            m.IsBinary,
			InformClientsOnLoss: m.InformClientsOnLoss,
			MessageId:           m.MessageID,
			Caller:              m.Caller,
			WaitForAck:          m.WaitForAck,
		})
	}
	return &pb.WsSend{CorrelationId: s.CorrelationID, Messages: messages}
}

func paramsFrom(values map[string]*pb.Values) handler.Params {
	if len(values) == 0 {
		return handler.Params{}
	}
	params := make(handler.Params, len(values))
	for name, v := range values {
		params[name] = v.GetValues()
	}
	return params
}

func paramsTo(params handler.Params) map[string]*pb.Values {
	if len(params) == 0 {
		return nil
	}
	values := make(map[string]*pb.Values, len(params))
	for name, v := range params {
		values[name] = &pb.Values{Values: v}
	}
	return values
}

// unixMillis treats zero as absent rather than as the epoch, so a dispatch
// without a deadline does not arrive already expired.
func unixMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
