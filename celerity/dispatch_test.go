package celerity_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// A handler tag names one source, so the runtime's own dispatch always carries
// the source a handler serves. Two things reach past that including the runtime's local
// invoke endpoint, which addresses any declared handler by name and dispatches
// every invocation as a custom one, and a blueprint whose annotations disagree
// with what the code registered. Both arrive here as an event carrying the
// wrong source.
type DispatchTestSuite struct {
	suite.Suite
}

func TestDispatchTestSuite(t *testing.T) {
	suite.Run(t, new(DispatchTestSuite))
}

func (s *DispatchTestSuite) Test_an_event_of_the_wrong_source_is_refused() {
	cases := []struct {
		name     string
		register func(app *celerity.App)
		tag      string
		event    *handler.Event
		serves   string
	}{
		{
			name: "an http handler reached as a custom invocation",
			register: func(app *celerity.App) {
				celerity.Get(app, "/orders", s.echo())
			},
			tag:    celerity.HTTPTag("GET", "/orders"),
			event:  &handler.Event{Kind: handler.KindCustom, Custom: &handler.CustomInvoke{}},
			serves: "http",
		},
		{
			// Would otherwise dereference a message that is not there.
			name: "a websocket handler reached as a custom invocation",
			register: func(app *celerity.App) {
				celerity.OnMessage(app, "sendMessage", s.echo())
			},
			tag:    celerity.WebSocketTag(celerity.DefaultWebSocketRouteKey, "sendMessage"),
			event:  &handler.Event{Kind: handler.KindCustom, Custom: &handler.CustomInvoke{}},
			serves: "websocket",
		},
		{
			// Likewise.
			name: "a custom handler reached as an http request",
			register: func(app *celerity.App) {
				celerity.Invoke(app, "recalculatePricing", s.echo())
			},
			tag:    celerity.CustomTag("recalculatePricing"),
			event:  &handler.Event{Kind: handler.KindHTTP, HTTP: &handler.Request{Method: "GET"}},
			serves: "custom",
		},
		{
			name: "a consumer reached as a custom invocation",
			register: func(app *celerity.App) {
				celerity.Consume(app, "orders", func(
					context.Context, *handler.ConsumerBatch,
				) (*handler.BatchResult, error) {
					return &handler.BatchResult{}, nil
				}, celerity.Named("processOrders"))
			},
			tag:    celerity.SourceTag("orders", "processOrders"),
			event:  &handler.Event{Kind: handler.KindCustom, Custom: &handler.CustomInvoke{}},
			serves: "consumer",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			app := celerity.New()
			tc.register(app)
			s.Require().NoError(app.Err())

			reg, ok := app.Registry().Get(tc.tag)
			s.Require().True(ok, "registered under %s", tc.tag)

			tc.event.Tag = tc.tag
			_, err := app.Pipeline(reg)(context.Background(), tc.event)

			s.Require().Error(err, "the handler ran on an event it does not serve")
			s.Contains(err.Error(), tc.serves, "the error should name what the handler serves")
			s.Contains(err.Error(), string(tc.event.Kind), "and what arrived instead")
		})
	}
}

func (s *DispatchTestSuite) Test_a_handler_still_serves_the_source_it_was_registered_for() {
	// The guard refuses what does not match, and nothing else.
	app := celerity.New()
	celerity.Get(app, "/orders", s.echo())
	s.Require().NoError(app.Err())

	reg, ok := app.Registry().Get(celerity.HTTPTag("GET", "/orders"))
	s.Require().True(ok)

	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		Kind: handler.KindHTTP,
		Tag:  celerity.HTTPTag("GET", "/orders"),
		HTTP: &handler.Request{Method: "GET", Path: "/orders"},
	})

	s.Require().NoError(err)
	s.Require().NotNil(res.HTTP)
	s.Equal(200, res.HTTP.Status)
}

func (s *DispatchTestSuite) echo() celerity.HandlerFunc[struct{}, struct{}] {
	return func(_ context.Context, in struct{}) (struct{}, error) { return in, nil }
}
