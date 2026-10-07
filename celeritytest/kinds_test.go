package celeritytest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/guard"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// The other four handler kinds, and the two things
// that wrap every kind consisting of a guard deciding
// whether an event reaches the handler at all, and a layer
// seeing it on the way in and out.
type KindsTestSuite struct {
	suite.Suite
}

func TestKindsTestSuite(t *testing.T) {
	suite.Run(t, new(KindsTestSuite))
}

type greeting struct {
	Name string `json:"name"`
}

type greeted struct {
	Message string `json:"message"`
}

// kindsApp registers one handler of every kind that is not HTTP, and records
// what each was given so a test can assert the harness delivered it.
type kindsApp struct {
	app       *celerity.App
	resources *celeritytest.Provider

	// seen is what each handler was handed, read after a dispatch.
	consumed  []string
	scheduled *handler.ScheduleTrigger
	messages  []string
	binary    bool
	// layered counts the dispatches a layer saw, which is every kind.
	layered int
	// identity is what a guard put on the context and the handler read back.
	identity any
	// observe is handed every event a layer saw, for a test asserting on what
	// the harness stamped it with.
	observe func(*handler.Event)
}

func buildKindsApp() *kindsApp {
	built := &kindsApp{resources: celeritytest.Resources()}

	counting := func(next layer.Next) layer.Next {
		return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
			built.layered++
			if built.observe != nil {
				built.observe(ev)
			}
			return next(ctx, ev)
		}
	}

	app := celerity.New(
		celerity.WithResourceProvider(built.resources),
		celerity.WithLayers(counting),
	)
	built.app = app

	celerity.AddGuard(app, "apiKey", func(ctx context.Context, req *guard.Request) (guard.Decision, error) {
		if req.Headers.Get("x-api-key") != "let-me-in" {
			return guard.Deny("an api key is required"), nil
		}
		return guard.Allow("the-caller"), nil
	})

	notifications := resources.Topic(app, "notifications")

	celerity.Consume(app, "ordersQueue",
		func(ctx context.Context, batch *handler.ConsumerBatch) (*handler.BatchResult, error) {
			result := &handler.BatchResult{}
			for _, record := range batch.Records {
				if string(record.Body) == "poison" {
					result.Fail(record.MessageID, errors.New("cannot be processed"))
					continue
				}
				built.consumed = append(built.consumed, string(record.Body))
			}
			return result, nil
		}, celerity.Named("processOrders"))

	celerity.Schedule(app, "nightly",
		func(ctx context.Context, trigger *handler.ScheduleTrigger) error {
			built.scheduled = trigger
			_, err := notifications.Publish(ctx, []byte("swept"))
			return err
		}, celerity.Named("nightlySweep"))

	celerity.Invoke(app, "greet",
		func(ctx context.Context, in greeting) (greeted, error) {
			if in.Name == "" {
				return greeted{}, errors.New("a name is required")
			}
			return greeted{Message: "hello " + in.Name}, nil
		}, celerity.Named("greet"))

	celerity.OnMessage(app, "sendMessage",
		func(ctx context.Context, in greeting) (struct{}, error) {
			message, ok := celerity.WebSocketEventFrom(ctx)
			if ok {
				built.binary = message.IsBinary
			}
			built.messages = append(built.messages, in.Name)
			return struct{}{}, nil
		}, celerity.Named("onSendMessage"))

	celerity.Get(app, "/secret",
		func(ctx context.Context, _ struct{}) (greeted, error) {
			decision, _ := guard.IdentityFrom(ctx)
			built.identity = decision.Identity
			return greeted{Message: "allowed"}, nil
		}, celerity.Named("getSecret"), celerity.ProtectedBy("apiKey"))

	celerity.Get(app, "/logged",
		func(ctx context.Context, _ struct{}) (greeted, error) {
			// The event-scoped logger is one of the two things a handler can
			// only reach through its context, so it is worth knowing the
			// harness leaves it reachable.
			telemetry.LoggerFrom(ctx).Info("serving")
			return greeted{Message: "logged"}, nil
		}, celerity.Named("getLogged"))

	return built
}

func (s *KindsTestSuite) Test_a_consumer_is_given_the_whole_batch() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	result := harness.Consume(s.T(), "processOrders", []byte("one"), []byte("two"))

	s.Equal([]string{"one", "two"}, built.consumed)
	s.False(result.Failed(), "nothing asked to be delivered again")
}

func (s *KindsTestSuite) Test_a_consumer_redrives_the_one_record_it_could_not_take() {
	// Failures are per record, so a batch where one fails redrives only that
	// one. Reaching that needs the whole batch at once, which is why a
	// consumer is dispatched a batch rather than a message.
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	result := harness.Consume(s.T(), "processOrders",
		[]byte("one"), []byte("poison"), []byte("three"))

	s.Equal([]string{"one", "three"}, built.consumed)
	s.Require().Len(result.Failures, 1, "the one record, rather than the batch")
}

func (s *KindsTestSuite) Test_a_consumer_takes_json_bodies_as_the_values_they_encode() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.ConsumeJSON(s.T(), "processOrders", greeting{Name: "ada"})

	s.Require().Len(built.consumed, 1)
	var decoded greeting
	s.Require().NoError(json.Unmarshal([]byte(built.consumed[0]), &decoded))
	s.Equal("ada", decoded.Name)
}

func (s *KindsTestSuite) Test_a_schedule_fires_with_the_rule_that_triggered_it() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.Schedule(s.T(), "nightlySweep")

	s.Require().NotNil(built.scheduled)
	s.Equal("nightly", built.scheduled.ScheduleID)
	s.Len(built.resources.TopicNamed("notifications").Published(), 1,
		"and what it did is there to read")
}

func (s *KindsTestSuite) Test_a_schedule_is_given_the_input_a_rule_carries() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.Schedule(s.T(), "nightlySweep", []byte(`{"depth":2}`))

	s.Require().NotNil(built.scheduled)
	s.JSONEq(`{"depth":2}`, string(built.scheduled.Input))
}

func (s *KindsTestSuite) Test_an_invoke_answers_the_caller() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	var answer greeted
	harness.Invoke(s.T(), "greet", greeting{Name: "ada"}, &answer)

	s.Equal("hello ada", answer.Message)
}

func (s *KindsTestSuite) Test_an_invoke_that_failed_stops_the_test_saying_why() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	failed := failureOf(func(tb testing.TB) {
		harness.Invoke(tb, "greet", greeting{}, nil)
	})

	s.Require().True(failed.stopped)
	s.Contains(failed.message, "a name is required")
}

func (s *KindsTestSuite) Test_a_websocket_message_reaches_the_handler_for_its_route() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.Send(s.T(), "sendMessage", []byte(`{"name":"ada"}`))

	s.Equal([]string{"ada"}, built.messages)
	s.False(built.binary, "a text frame unless the test says otherwise")
}

func (s *KindsTestSuite) Test_a_websocket_message_can_arrive_as_a_binary_frame() {
	// A byte body alone cannot express the distinction, and the protocol
	// treats the two frame types as distinct.
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.Send(s.T(), "sendMessage", []byte(`{"name":"ada"}`), celeritytest.Binary())

	s.True(built.binary)
}

func (s *KindsTestSuite) Test_a_guard_refuses_an_event_before_it_reaches_the_handler() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	response := harness.GET(s.T(), "/secret")

	s.Equal(http.StatusUnauthorized, response.Status(),
		"the guard answered rather than the handler")
	s.Nil(built.identity, "which never ran")
}

func (s *KindsTestSuite) Test_a_guard_that_allows_puts_the_identity_on_the_context() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	response := harness.GET(s.T(), "/secret",
		celeritytest.Header("X-API-Key", "let-me-in"))

	response.AssertStatus(s.T(), http.StatusOK)
	s.Equal("the-caller", built.identity)
}

func (s *KindsTestSuite) Test_an_application_layer_sees_every_kind_of_dispatch() {
	// Layers wrap the handler whatever the event is, so a layer counting
	// dispatches is what shows the harness built the same pipeline for each.
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.Consume(s.T(), "processOrders", []byte("one"))
	harness.Schedule(s.T(), "nightlySweep")
	harness.Invoke(s.T(), "greet", greeting{Name: "ada"}, nil)
	harness.Send(s.T(), "sendMessage", []byte(`{"name":"ada"}`))
	harness.GET(s.T(), "/logged")

	s.Equal(5, built.layered, "one for each kind")
}

func (s *KindsTestSuite) Test_a_handler_reaches_the_logger_through_its_context() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	harness.GET(s.T(), "/logged").AssertStatus(s.T(), http.StatusOK)
}

func (s *KindsTestSuite) Test_a_handler_of_the_wrong_kind_is_not_found_by_name() {
	// getSecret is registered, but as an HTTP handler, so asking for it as a
	// consumer has to say so rather than dispatching the wrong thing.
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app)

	failed := failureOf(func(tb testing.TB) { harness.Consume(tb, "getSecret") })

	s.Require().True(failed.stopped)
	s.Contains(failed.message, `no consumer handler is registered as "getSecret"`)
	s.Contains(failed.message, "processOrders", "and what is")
}

// The harness options, which decide what an event carries rather than what a
// handler does with it.
func (s *KindsTestSuite) Test_a_fixed_clock_is_what_an_event_is_stamped_with() {
	built := buildKindsApp()
	at := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	harness := celeritytest.New(s.T(), built.app,
		celeritytest.WithClock(func() time.Time { return at }),
		celeritytest.WithDeadline(time.Minute))

	var seen *handler.Event
	built.observe = func(ev *handler.Event) { seen = ev }
	harness.GET(s.T(), "/logged")

	s.Require().NotNil(seen)
	s.Equal(at, seen.Timestamp)
	s.Equal(at.Add(time.Minute), seen.Deadline, "the deadline falls after the timestamp")
}

func (s *KindsTestSuite) Test_a_trace_context_arrives_with_every_dispatch() {
	built := buildKindsApp()
	harness := celeritytest.New(s.T(), built.app, celeritytest.WithTraceContext(
		map[string]string{telemetry.TraceParentHeader: "00-abc-def-01"}))

	var seen *handler.Event
	built.observe = func(ev *handler.Event) { seen = ev }
	harness.GET(s.T(), "/logged")

	s.Require().NotNil(seen)
	s.Equal("00-abc-def-01", seen.TraceContext[telemetry.TraceParentHeader])
}

func (s *KindsTestSuite) Test_the_resources_a_harness_was_given_are_reachable_from_it() {
	built := buildKindsApp()
	harness := celeritytest.NewWithResources(s.T(), built.app, built.resources)

	harness.Schedule(s.T(), "nightlySweep")

	s.Len(harness.Resources().TopicNamed("notifications").Published(), 1)
}
