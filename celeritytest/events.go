package celeritytest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Consume delivers a batch of messages to the consumer handler registered under
// a blueprint name, and reports which records it asked to be delivered again.
//
// The bodies are delivered as one batch, which is how a consumer is called: a
// handler that fails one record of several is the case worth testing, and it
// cannot be reached a message at a time.
func (a *App) Consume(t testing.TB, name string, bodies ...[]byte) *handler.BatchResult {
	t.Helper()

	reg := a.handlerNamed(t, name, handler.KindConsumer)
	records := make([]handler.ConsumerRecord, 0, len(bodies))
	for _, body := range bodies {
		records = append(records, handler.ConsumerRecord{
			MessageID: a.nextID("message"),
			Body:      body,
			Source:    reg.SourceID,
		})
	}

	event := a.event(reg, a.nextID("event"))
	event.Consumer = &handler.ConsumerBatch{Records: records, SourceID: reg.SourceID}

	result, err := a.dispatch(t, reg, event)
	if err == nil {
		err = resultError(result)
	}
	if err != nil {
		t.Fatalf("celeritytest: %s failed: %v", name, err)
	}
	if result.Consumer == nil {
		// A consumer that reports nothing has taken the whole batch, which is
		// the ordinary answer rather than a missing one.
		return &handler.BatchResult{}
	}
	return result.Consumer
}

// ConsumeJSON delivers a batch whose bodies are the encodings of the values
// given, which is how a message is usually carried.
func (a *App) ConsumeJSON(t testing.TB, name string, messages ...any) *handler.BatchResult {
	t.Helper()

	bodies := make([][]byte, 0, len(messages))
	for _, message := range messages {
		encoded, err := json.Marshal(message)
		if err != nil {
			t.Fatalf("celeritytest: encoding a message for %s: %v", name, err)
		}
		bodies = append(bodies, encoded)
	}
	return a.Consume(t, name, bodies...)
}

// Schedule fires the schedule handler registered under a blueprint name.
func (a *App) Schedule(t testing.TB, name string, input ...[]byte) {
	t.Helper()

	reg := a.handlerNamed(t, name, handler.KindSchedule)
	trigger := &handler.ScheduleTrigger{
		ScheduleID: reg.SourceID,
		MessageID:  a.nextID("message"),
	}
	if len(input) > 0 {
		trigger.Input = input[0]
	}

	event := a.event(reg, a.nextID("event"))
	event.Schedule = trigger

	result, err := a.dispatch(t, reg, event)
	if err == nil {
		err = resultError(result)
	}
	if err != nil {
		t.Fatalf("celeritytest: %s failed: %v", name, err)
	}
	if result.Schedule != nil && !result.Schedule.Success {
		t.Fatalf("celeritytest: %s did not succeed: %s", name, result.Schedule.Error)
	}
}

// Invoke calls a handler directly by its blueprint name and decodes its answer
// into out, which may be nil where the answer is not wanted.
func (a *App) Invoke(t testing.TB, name string, input any, out any) {
	t.Helper()

	reg := a.handlerNamed(t, name, handler.KindCustom)

	var encoded []byte
	if input != nil {
		var err error
		if encoded, err = json.Marshal(input); err != nil {
			t.Fatalf("celeritytest: encoding the input for %s: %v", name, err)
		}
	}

	event := a.event(reg, a.nextID("event"))
	event.Custom = &handler.CustomInvoke{HandlerName: reg.Name, Input: encoded}

	result, err := a.dispatch(t, reg, event)
	if err == nil {
		err = resultError(result)
	}
	if err != nil {
		t.Fatalf("celeritytest: %s failed: %v", name, err)
	}
	if out == nil {
		return
	}
	if result.Custom == nil {
		t.Fatalf("celeritytest: %s answered with nothing to decode", name)
	}
	if err := json.Unmarshal(result.Custom.Output, out); err != nil {
		t.Fatalf("celeritytest: reading %s's answer as %T: %v\noutput: %s",
			name, out, err, result.Custom.Output)
	}
}

// WebSocketMessage configures a message before it is delivered.
type WebSocketMessage func(*handler.WebSocketMessage)

// From sets the connection a message arrives on, which is what a handler
// answers back to.
func From(connectionID string) WebSocketMessage {
	return func(m *handler.WebSocketMessage) {
		m.ConnectionID = connectionID
	}
}

// Binary delivers the message as a binary frame rather than a text one.
func Binary() WebSocketMessage {
	return func(m *handler.WebSocketMessage) {
		m.IsBinary = true
	}
}

// Send delivers a WebSocket message to the handler registered for a route.
//
// The route is the one the message's route key names, which is how the runtime
// picks a handler, so a test names what a client would send.
func (a *App) Send(t testing.TB, route string, body []byte, opts ...WebSocketMessage) {
	t.Helper()

	reg := a.webSocketRoute(t, route)
	message := &handler.WebSocketMessage{
		Route:        route,
		ConnectionID: "connection-1",
		RequestID:    a.nextID("request"),
		MessageID:    a.nextID("message"),
		Message:      body,
	}
	for _, opt := range opts {
		opt(message)
	}

	event := a.event(reg, a.nextID("event"))
	event.WebSocket = message

	result, err := a.dispatch(t, reg, event)
	if err == nil {
		err = resultError(result)
	}
	if err != nil {
		t.Fatalf("celeritytest: the handler for %q failed: %v", route, err)
	}
	if result.WebSocket != nil && !result.WebSocket.Success {
		t.Fatalf("celeritytest: the handler for %q did not succeed: %s",
			route, result.WebSocket.Error)
	}
}

// Finds the handler a blueprint calls name, reporting what is
// registered of that kind when nothing matches.
func (a *App) handlerNamed(t testing.TB, name string, kind handler.Kind) *celerity.Registration {
	t.Helper()

	if reg, ok := a.app.Registry().ByName(name); ok && reg.Kind == kind {
		return reg
	}

	var registered []string
	for _, reg := range a.app.Registry().OfKind(kind) {
		registered = append(registered, reg.Name)
	}
	t.Fatalf("celeritytest: no %s handler is registered as %q.\nRegistered: %s",
		kind, name, strings.Join(registered, ", "))
	return nil
}

// Finds the handler a route reaches.
//
// Matched on the route rather than the route key: the key is the field of the
// message the runtime reads the route out of, and is the application's, while
// the route is the value that picks one handler from another.
func (a *App) webSocketRoute(t testing.TB, route string) *celerity.Registration {
	t.Helper()

	var registered []string
	for _, reg := range a.app.Registry().OfKind(handler.KindWebSocket) {
		registered = append(registered, reg.Route)
		if reg.Route == route {
			return reg
		}
	}

	t.Fatalf("celeritytest: no WebSocket handler is registered for %q.\nRegistered: %s",
		route, strings.Join(registered, ", "))
	return nil
}

func resultError(result *handler.Result) error {
	if result.Error == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", result.Error.Type, result.Error.Message)
}
