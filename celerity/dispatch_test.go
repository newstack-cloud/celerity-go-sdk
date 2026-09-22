package celerity_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celerity"
	"github.com/newstack-cloud/celerity-go-sdk/config"
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

// Configuration is a dependency rather than anything about a request, so a
// handler takes it as an argument the way it takes a store or a client.
func (s *DispatchTestSuite) Test_a_handler_is_given_configuration_as_an_argument() {
	cfg := configWith(map[string]string{"REGION": "eu-west-2"})
	app := celerity.New()

	var got string
	celerity.Get(app, "/orders", readRegion(cfg, &got))
	s.Require().NoError(app.Err())

	s.Require().NoError(s.dispatch(app, celerity.HTTPTag("GET", "/orders")))
	s.Equal("eu-west-2", got)
}

func (s *DispatchTestSuite) Test_a_supplied_service_is_used_rather_than_the_environment() {
	// An environment that describes a store of its own, holding a different
	// value under the same key. This helps prove that what has been selected
	// is the supplied override.
	s.T().Setenv("CELERITY_CONFIG_STORE_ID", "the-deployments-store")
	s.T().Setenv(config.PlatformEnvVar, "gcp")

	fromEnvironment, err := config.FromEnvironment()
	s.Require().NoError(err)
	value, err := fromEnvironment.Get(context.Background(), "REGION")
	s.Require().NoError(err)
	s.Require().Equal("from-the-environment", value,
		"the environment has to hold a different value, or this proves nothing")

	supplied := configWith(map[string]string{"REGION": "eu-west-2"})

	app := celerity.New(celerity.WithConfig(supplied))

	// The service the application holds, which is what a resource provider
	// reads while handles are being taken.
	s.Same(supplied, app.Config())
	s.Equal([]string{"settings"}, app.Config().Registered(),
		"the environment's own namespace should not be there")

	got, err := app.Config().Get(context.Background(), "REGION")
	s.Require().NoError(err)
	s.Equal("eu-west-2", got)
}

func (s *DispatchTestSuite) Test_the_deployments_stores_are_wired_up_without_being_asked_for() {
	// The ordinary route where an application doesn't need to configure anything,
	// and the store its deployment described is registered.
	s.T().Setenv("CELERITY_CONFIG_STORE_ID", "orders-config-store")
	s.T().Setenv(config.PlatformEnvVar, "local")

	app := celerity.New()
	s.Require().NoError(app.Err())

	s.Equal([]string{config.DefaultNamespace}, app.Config().Registered())

	// Reading it needs a provider for the platform, and this binary doesn't link
	// any, so a value is absent rather than the application failing to start.
	_, ok, err := app.Config().Lookup(context.Background(), "REGION")
	s.Require().NoError(err)
	s.False(ok)
}

func (s *DispatchTestSuite) Test_an_application_whose_deployment_described_no_store() {
	// A service with no namespaces rather than nothing, so a provider reading
	// it is told the application declares no config resource rather than
	// having to guard against nil.
	app := celerity.New()

	s.Require().NotNil(app.Config())
	s.Empty(app.Config().Registered())

	_, err := app.Config().Get(context.Background(), "REGION")
	s.Require().Error(err)
	s.Contains(err.Error(), "celerity/config")
}

// A handler built around the configuration it needs, which is how
// every other dependency reaches a handler in this SDK.
func readRegion(cfg *config.Service, into *string) celerity.HandlerFunc[struct{}, struct{}] {
	return func(ctx context.Context, _ struct{}) (struct{}, error) {
		value, err := cfg.Get(ctx, "REGION")
		*into = value
		return struct{}{}, err
	}
}

// environmentProvider stands in for a platform's own config store, so a case
// can set up an environment that really does hold a value and check which one
// an application ends up reading.
type environmentProvider struct{}

func (environmentProvider) Name() string              { return "fake-environment" }
func (environmentProvider) Platform() config.Platform { return config.PlatformGCP }

func (environmentProvider) Backend(config.StoreKind) (config.Backend, error) {
	return config.MapBackend{
		"the-deployments-store": {"REGION": "from-the-environment"},
	}, nil
}

func init() { config.RegisterProvider(environmentProvider{}) }

func configWith(values map[string]string) *config.Service {
	svc := config.New()
	svc.Register("settings", config.NewNamespace(
		config.MapBackend{"settings": values}, "settings"))
	return svc
}

// dispatch runs one HTTP event through a registered handler's pipeline.
func (s *DispatchTestSuite) dispatch(app *celerity.App, tag string) error {
	reg, ok := app.Registry().Get(tag)
	s.Require().True(ok, "registered under %s", tag)

	_, err := app.Pipeline(reg)(context.Background(), &handler.Event{
		Kind: handler.KindHTTP,
		Tag:  tag,
		HTTP: &handler.Request{Method: "GET", Path: "/orders"},
	})
	return err
}
