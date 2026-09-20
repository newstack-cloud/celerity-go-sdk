package serverless_test

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// A stand-in provider, so that what the serverless package decides is
// exercised without a platform.
//
// The mapper is a separate type because it must be: [serverless.Adapter.Detect]
// reports the platform and [serverless.EventMapper.Detect] reports the source,
// so one type cannot be both. Every real adapter is shaped this way too.
type dispatchAdapter struct {
	kind   handler.Kind
	sender handler.WebSocketSender
	ackErr error

	acked bool
}

func (a *dispatchAdapter) Name() string { return "dispatch-fake" }
func (a *dispatchAdapter) Detect() bool { return true }

func (a *dispatchAdapter) Mapper() serverless.EventMapper {
	return fakeMapper{kind: a.kind}
}

func (a *dispatchAdapter) Start(context.Context, serverless.Invoker) error { return nil }

func (a *dispatchAdapter) WebSocketSender(
	context.Context,
	[]byte,
) (handler.WebSocketSender, error) {
	if a.sender == nil {
		return nil, errNoSender
	}
	return a.sender, nil
}

// acknowledgingAdapter is the adapter for a transport that leaves
// acknowledging a client's message to the SDK. The plain one deliberately does
// not implement [serverless.ReceiptAcknowledger], which is what the case about
// an adapter that acknowledges nothing turns on.
type acknowledgingAdapter struct {
	*dispatchAdapter
}

func (a *acknowledgingAdapter) AcknowledgeReceipt(
	context.Context,
	*handler.WebSocketMessage,
) error {
	a.acked = true
	return a.ackErr
}

// fakeMapper maps nothing meaningful: every case here turns on resolution, on
// what an unroutable event means, or on the order the dispatcher does things
// in, none of which is a mapper's concern.
type fakeMapper struct{ kind handler.Kind }

func (m fakeMapper) Detect([]byte) (handler.Kind, error) { return m.kind, nil }

func (m fakeMapper) ToRequest([]byte) (*handler.Request, error) {
	return &handler.Request{Method: "GET", Path: "/orders", RequestID: "req-1"}, nil
}

func (m fakeMapper) FromResponse(res *handler.Response) (any, error) { return res, nil }

func (m fakeMapper) ToWebSocketMessage([]byte) (*handler.WebSocketMessage, error) {
	return &handler.WebSocketMessage{
		Route:        "sendMessage",
		ConnectionID: "conn-1",
		MessageID:    "msg-1",
	}, nil
}

func (m fakeMapper) ToConsumerBatch([]byte, string) (*handler.ConsumerBatch, error) {
	return &handler.ConsumerBatch{}, nil
}

func (m fakeMapper) FromBatchResult(res *handler.BatchResult) (any, error) { return res, nil }

func (m fakeMapper) ToScheduleTrigger([]byte, string) (*handler.ScheduleTrigger, error) {
	return &handler.ScheduleTrigger{}, nil
}

func (m fakeMapper) ToCustomInvoke([]byte) (*handler.CustomInvoke, error) {
	return &handler.CustomInvoke{}, nil
}

func (m fakeMapper) FromCustomResult(res *handler.CustomInvokeResult) (any, error) {
	return res, nil
}

// fakeResolver stands in for the application's registry, recording which
// handler the dispatcher asked for and how often it asked.
type fakeResolver struct {
	byName map[string]string
	byTag  map[string]bool
	only   string

	observe func(ctx context.Context)

	invoked string
	lookups int
}

func (r *fakeResolver) ByName(name string) (*serverless.Handler, bool) {
	tag, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	return r.handler(tag), true
}

func (r *fakeResolver) ByTag(tag string) (*serverless.Handler, bool) {
	if !r.byTag[tag] {
		return nil, false
	}
	return r.handler(tag), true
}

func (r *fakeResolver) Only(kind handler.Kind) (*serverless.Handler, bool) {
	if r.only == "" {
		return nil, false
	}
	h := r.handler(r.only)
	h.Kind = kind
	return h, true
}

func (r *fakeResolver) handler(tag string) *serverless.Handler {
	r.lookups++
	return &serverless.Handler{Tag: tag, Invoke: r.invoke(tag)}
}

func (r *fakeResolver) invoke(tag string) layer.Next {
	return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
		r.invoked = tag
		if r.observe != nil {
			r.observe(ctx)
		}
		return resultFor(ev), nil
	}
}

// resultFor answers with the outcome the event's own source takes, since the
// dispatcher maps a result by kind and a mismatched one would be reported as an
// unsupported source rather than as the answer it is.
func resultFor(ev *handler.Event) *handler.Result {
	res := &handler.Result{ID: ev.ID}
	switch ev.Kind {
	case handler.KindHTTP:
		res.HTTP = &handler.Response{Status: 200}
	case handler.KindWebSocket:
		res.WebSocket = &handler.Ack{Success: true}
	case handler.KindConsumer:
		res.Consumer = &handler.BatchResult{}
	case handler.KindSchedule:
		res.Schedule = &handler.Ack{Success: true}
	case handler.KindCustom:
		res.Custom = &handler.CustomInvokeResult{}
	}
	return res
}

type countingSender struct{ sends int }

func (s *countingSender) Send(context.Context, ...handler.OutboundMessage) error {
	s.sends++
	return nil
}

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

const errNoSender = sentinelError("no sender")
