package celerity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/guard"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// A handler registered by name takes its routing from the blueprint.
// Everything the blueprint has no concept of stays in code.
type BlueprintHandlerTestSuite struct {
	suite.Suite
}

func TestBlueprintHandlerTestSuite(t *testing.T) {
	suite.Run(t, new(BlueprintHandlerTestSuite))
}

type lookup struct {
	OrderID string `json:"orderId" path:"orderId"`
}

func (s *BlueprintHandlerTestSuite) noop() celerity.HandlerFunc[lookup, lookup] {
	return func(_ context.Context, in lookup) (lookup, error) { return in, nil }
}

func (s *BlueprintHandlerTestSuite) Test_the_tag_comes_from_the_blueprint() {
	app := celerity.New()
	celerity.Handler(app, "getOrderHandler", s.noop())
	s.Require().NoError(app.Err())

	// Before reconciliation it holds a placeholder, which no runtime declares.
	s.NotContains(app.Registry().Tags(), "GET::/orders/{orderId}")

	tags, err := app.ReconcileTags([]celerity.BlueprintHandler{
		{Name: "getOrderHandler", Tag: "GET::/orders/{orderId}"},
	})

	s.Require().NoError(err)
	s.Equal([]string{"GET::/orders/{orderId}"}, tags)

	reg, ok := app.Registry().Get("GET::/orders/{orderId}")
	s.Require().True(ok, "the registry should serve the blueprint's tag")
	s.Equal(handler.KindHTTP, reg.Kind, "the kind should be read from the tag")
}

func (s *BlueprintHandlerTestSuite) Test_the_kind_is_read_from_the_tag_shape() {
	cases := []struct {
		name string
		tag  string
		want handler.Kind
	}{
		{"an http route", "POST::/orders", handler.KindHTTP},
		{"a websocket route", "event::sendMessage", handler.KindWebSocket},
		{"a custom invocation", "custom::recalculatePricing", handler.KindCustom},
		{"a queue or schedule source", "source::orderQueue::process", handler.KindConsumer},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			app := celerity.New()
			celerity.Handler(app, "theHandler", s.noop())
			s.Require().NoError(app.Err())

			_, err := app.ReconcileTags([]celerity.BlueprintHandler{
				{Name: "theHandler", Tag: tc.tag},
			})
			s.Require().NoError(err)

			reg, ok := app.Registry().Get(tc.tag)
			s.Require().True(ok)
			s.Equal(tc.want, reg.Kind)
		})
	}
}

func (s *BlueprintHandlerTestSuite) Test_a_name_the_blueprint_does_not_declare_is_refused() {
	app := celerity.New()
	celerity.Handler(app, "getOrdrHandler", s.noop()) // a typo
	s.Require().NoError(app.Err())

	_, err := app.ReconcileTags([]celerity.BlueprintHandler{
		{Name: "getOrderHandler", Tag: "GET::/orders/{orderId}"},
	})

	var unresolved *celerity.UnresolvedHandlerError
	s.Require().ErrorAs(err, &unresolved)
	s.Equal([]string{"getOrdrHandler"}, unresolved.Names)

	// The declared names are listed, which is what a typo is spotted against.
	s.Equal([]string{"getOrderHandler"}, unresolved.Declared)
	s.Contains(err.Error(), "getOrdrHandler")
	s.Contains(err.Error(), "getOrderHandler")
}

func (s *BlueprintHandlerTestSuite) Test_a_published_name_also_resolves() {
	// A deployment addresses a handler by spec.handlerName, so a handler
	// registered under that name has to find itself too.
	app := celerity.New()
	celerity.Handler(app, "Orders-GetOrder-v1", s.noop())
	s.Require().NoError(app.Err())

	_, err := app.ReconcileTags([]celerity.BlueprintHandler{
		{Name: "getOrderHandler", PublishedName: "Orders-GetOrder-v1", Tag: "GET::/orders"},
	})

	s.Require().NoError(err)
	_, ok := app.Registry().Get("GET::/orders")
	s.True(ok)
}

func (s *BlueprintHandlerTestSuite) Test_code_side_options_survive_blueprint_wiring() {
	app := celerity.New(celerity.WithValidator(stubValidator{issues: []handler.ValidationIssue{
		{Code: "required", Path: []string{"orderId"}, Message: "is required"},
	}}))
	celerity.AddGuard(app, "jwt", func(context.Context, *guard.Request) (guard.Decision, error) {
		return guard.Allow("someone"), nil
	})

	var layerRan bool
	celerity.Handler(app, "getOrderHandler", s.noop(),
		celerity.ProtectedBy("jwt"),
		celerity.MaxConcurrent(2),
		celerity.With(func(next layer.Next) layer.Next {
			return func(ctx context.Context, ev *handler.Event) (*handler.Result, error) {
				layerRan = true
				return next(ctx, ev)
			}
		}),
	)
	s.Require().NoError(app.Err())

	tags, err := app.ReconcileTags([]celerity.BlueprintHandler{
		{Name: "getOrderHandler", Tag: "GET::/orders/{orderId}"},
	})
	s.Require().NoError(err)
	s.Require().Equal([]string{"GET::/orders/{orderId}"}, tags)

	// The cap follows the reconciled tag, as it does for a code-routed handler.
	s.Equal(map[string]int{"GET::/orders/{orderId}": 2}, app.HandlerLimits())

	reg, _ := app.Registry().Get("GET::/orders/{orderId}")
	s.Equal([]string{"jwt"}, reg.Guards)

	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "GET", Body: []byte(`{}`)},
	})
	s.Require().NoError(err)

	s.True(layerRan, "a layer given in code should still run")
	// And the application validator still applies.
	s.Equal(400, res.HTTP.Status)

	var answer struct {
		Details []handler.ValidationIssue `json:"details"`
	}
	s.Require().NoError(json.Unmarshal(res.HTTP.Body, &answer))
	s.Require().Len(answer.Details, 1)
	s.Equal("required", answer.Details[0].Code)
}

func (s *BlueprintHandlerTestSuite) Test_validation_can_be_skipped_for_a_blueprint_handler() {
	app := celerity.New(celerity.WithValidator(stubValidator{issues: []handler.ValidationIssue{
		{Code: "required", Path: []string{"orderId"}, Message: "is required"},
	}}))

	celerity.Handler(app, "forwardPayload", s.noop(), celerity.SkipValidation())
	s.Require().NoError(app.Err())

	_, err := app.ReconcileTags([]celerity.BlueprintHandler{
		{Name: "forwardPayload", Tag: "POST::/forward"},
	})
	s.Require().NoError(err)

	reg, _ := app.Registry().Get("POST::/forward")
	res, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindHTTP,
		HTTP: &handler.Request{Method: "POST", Body: []byte(`{}`)},
	})

	s.Require().NoError(err)
	s.Equal(200, res.HTTP.Status, "the handler should have run")
}

// Consumer and schedule handlers take a batch and a trigger, not a decoded
// input, so a blueprint binding one of those to a typed handler is a mismatch
// worth naming.
func (s *BlueprintHandlerTestSuite) Test_a_source_bound_to_a_typed_handler_is_reported() {
	app := celerity.New()
	celerity.Handler(app, "processOrders", s.noop())
	s.Require().NoError(app.Err())

	_, err := app.ReconcileTags([]celerity.BlueprintHandler{
		{Name: "processOrders", Tag: "source::orderQueue::processOrders"},
	})
	s.Require().NoError(err)

	reg, _ := app.Registry().Get("source::orderQueue::processOrders")
	_, err = app.Pipeline(reg)(context.Background(), &handler.Event{
		ID: "event-1", Kind: handler.KindConsumer,
		Consumer: &handler.ConsumerBatch{},
	})

	s.Require().Error(err)
	s.Contains(err.Error(), "Consume or Schedule")
}
