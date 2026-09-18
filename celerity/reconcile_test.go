package celerity_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
)

// A WebSocket tag is {routeKey}::{route}. Code states the route, matching the
// other SDKs; the route key belongs to the API, so it comes from the blueprint
// when there is one to ask.
type ReconcileTestSuite struct {
	suite.Suite
}

func TestReconcileTestSuite(t *testing.T) {
	suite.Run(t, new(ReconcileTestSuite))
}

// declaring turns tags into what the runtime reports, naming each handler after
// the route so a name-based lookup has something to find.
func declaring(tags ...string) []celerity.BlueprintHandler {
	declared := make([]celerity.BlueprintHandler, 0, len(tags))
	for _, tag := range tags {
		name := tag
		if _, route, found := strings.Cut(tag, "::"); found {
			name = route
		}
		declared = append(declared, celerity.BlueprintHandler{Name: name, Tag: tag})
	}
	return declared
}

func (s *ReconcileTestSuite) noopHandler() celerity.HandlerFunc[map[string]any, map[string]any] {
	return func(_ context.Context, in map[string]any) (map[string]any, error) { return in, nil }
}

// Registers the three WebSocket kinds, which is enough to show what
// reconciliation does and does not touch.
func (s *ReconcileTestSuite) wsApp(opts ...celerity.Option) *celerity.App {
	app := celerity.New(opts...)
	noop := s.noopHandler()

	celerity.OnConnect(app, noop, celerity.Named("connectHandler"))
	celerity.OnMessage(app, "sendMessage", noop, celerity.Named("sendMessageHandler"))
	celerity.OnDisconnect(app, noop, celerity.Named("disconnectHandler"))

	s.Require().NoError(app.Err(), "registering")
	return app
}

func (s *ReconcileTestSuite) Test_routes_are_declared_in_code() {
	// The default key is used until something authoritative says otherwise, so
	// the tags are well formed with no blueprint at all.
	app := s.wsApp()

	s.Equal(
		[]string{"event::$connect", "event::$disconnect", "event::sendMessage"},
		app.Registry().Tags(),
	)
}

func (s *ReconcileTestSuite) Test_the_blueprints_route_key_is_adopted() {
	// The API is keyed by "action" rather than the default, which code has no
	// way of knowing. The runtime states it, so the handler adopts it.
	blueprint := []string{"action::$connect", "action::$disconnect", "action::sendMessage"}

	app := s.wsApp()
	got, err := app.ReconcileTags(declaring(blueprint...))

	s.Require().NoError(err)
	s.Equal(blueprint, got)

	// The registry has to agree, or dispatch would not route to the handler.
	_, serves := app.Registry().Get("action::sendMessage")
	s.True(serves, "the registry does not serve the reconciled tag")

	_, stale := app.Registry().Get("event::sendMessage")
	s.False(stale, "the registry still serves the tag built before reconciliation")
}

func (s *ReconcileTestSuite) Test_an_explicit_route_key_is_used_when_the_blueprint_says_nothing() {
	// Serverless has no runtime to ask, so the option is what carries it.
	app := s.wsApp(celerity.WithWebSocketRouteKey("action"))

	_, ok := app.Registry().Get("action::sendMessage")
	s.True(ok, "tags = %v, want the configured route key", app.Registry().Tags())
}

func (s *ReconcileTestSuite) Test_an_unmatched_route_keeps_its_own_tag() {
	// Left as declared rather than guessed at, so the handshake reports the tag
	// the handler actually serves and the mismatch is legible.
	app := s.wsApp()

	got, err := app.ReconcileTags(declaring("action::somethingElse"))

	s.Require().NoError(err)
	s.Contains(got, "event::sendMessage")
}

func (s *ReconcileTestSuite) Test_reconciliation_leaves_other_handler_kinds_alone() {
	app := celerity.New()
	noop := s.noopHandler()

	celerity.Get(app, "/orders", noop, celerity.Named("getOrders"))
	celerity.OnMessage(app, "sendMessage", noop, celerity.Named("sendMessageHandler"))
	s.Require().NoError(app.Err())

	// A consumer tag is source::{sourceId}::{handlerName}, which splitting from
	// the wrong end would read as a route.
	got, err := app.ReconcileTags(declaring(
		"GET::/orders",
		"source::orderQueue::processOrder",
		"action::sendMessage",
	))
	s.Require().NoError(err)

	s.Contains(got, "GET::/orders", "the HTTP tag should be untouched")
	s.Contains(got, "action::sendMessage", "the websocket tag should be reconciled")
}

func (s *ReconcileTestSuite) Test_per_handler_caps_are_declared() {
	app := celerity.New()
	noop := s.noopHandler()

	celerity.Get(app, "/orders", noop, celerity.Named("listOrders"))
	celerity.Post(app, "/reports", noop,
		celerity.Named("buildReport"), celerity.MaxConcurrent(2))
	s.Require().NoError(app.Err())

	s.Equal(map[string]int{"POST::/reports": 2}, app.HandlerLimits())
}

func (s *ReconcileTestSuite) Test_caps_follow_a_reconciled_tag() {
	// Reconciliation can move a WebSocket handler to the tag the blueprint
	// declares. A cap keyed by the tag built beforehand would name a handler
	// the runtime does not know, and would be silently ignored.
	app := celerity.New()

	celerity.OnMessage(app, "sendMessage", s.noopHandler(),
		celerity.Named("sendMessageHandler"), celerity.MaxConcurrent(3))
	s.Require().NoError(app.Err())
	s.Require().Equal(3, app.HandlerLimits()["event::sendMessage"])

	_, err := app.ReconcileTags(declaring("action::sendMessage"))
	s.Require().NoError(err)

	after := app.HandlerLimits()
	s.Equal(3, after["action::sendMessage"], "the cap should be keyed by the reconciled tag")
	s.NotContains(after, "event::sendMessage",
		"no cap should remain under the tag built before reconciliation")
}
