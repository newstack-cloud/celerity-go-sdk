// Package celerity is the entry point to the Celerity Go SDK: it holds the
// application, the registration API, and the handler registry those produce.
//
// Registration is explicit rather than derived from decorators or struct tags,
// which is what the Node and Python SDKs use and what Go has no equivalent of.
// Dependencies reach a handler through the closure or the receiver, the way
// they do in any Go program:
//
//	app := celerity.New()
//	celerity.Get(app, "/orders/{orderId}", orders.Get(store))
//	celerity.Run(app)
package celerity

import (
	"errors"
	"runtime"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// App is one Celerity application: everything it serves, and how it should
// serve it.
type App struct {
	registry *Registry
	options  options

	// Registration errors are collected rather than returned, so that a file of
	// registrations reads as a list. Run reports them together.
	errs []error

	resourceRefs map[resources.Kind]map[string]struct{}
}

type options struct {
	layers            []layer.Layer
	guards            map[string]Guard
	adapter           serverless.Adapter
	logger            telemetry.Logger
	resourceProvider  resources.Provider
	webSocketRouteKey string
	validator         Validator
	config            *config.Service
}

// Option configures an application.
type Option func(*options)

// WithLayers adds application-scoped layers, which wrap every handler.
func WithLayers(layers ...layer.Layer) Option {
	return func(o *options) { o.layers = append(o.layers, layers...) }
}

// WithAdapter supplies the serverless adapter explicitly.
//
// Applications do not normally need it: importing an adapter registers it, and
// the one whose platform the process is running on is selected at startup. It
// is here for a test that wants to drive a fake adapter.
func WithAdapter(a serverless.Adapter) Option {
	return func(o *options) { o.adapter = a }
}

// WithWebSocketRouteKey sets the field in a WebSocket message that carries the
// route, which is a property of the API rather than of any handler:
//
//	{"action": "sendMessage", "data": {...}}
//
// Under the Celerity runtime this is a fallback. The blueprint states it, and
// the runtime sends every declared handler tag before the handshake, so the
// tags are reconciled against what the blueprint actually says. It matters
// where there is no runtime to ask, which is every serverless deployment.
func WithWebSocketRouteKey(key string) Option {
	return func(o *options) { o.webSocketRouteKey = key }
}

// WithConfig supplies the configuration service explicitly.
//
// Applications do not normally need it as the stores a blueprint's
// celerity/config resources were deployed as are described in the environment,
// and reading them needs whichever provider module is linked, so
// [config.FromEnvironment] does this at startup. It is here for a test and for
// a local run that wants to read from a map rather than reach a real store:
//
//	cfg := config.New()
//	cfg.Register("settings", config.NewNamespace(
//		config.MapBackend{"settings": {"REGION": "eu-west-2"}}, "settings"))
//
//	app := celerity.New(celerity.WithConfig(cfg))
//
// The service is not put in a handler's context. It exists before any event
// does and is the same object for every one of them, so a handler that reads
// configuration is given it, the way it is given a store or a client:
//
//	celerity.Get(app, "/orders", orders.List(store, cfg))
func WithConfig(svc *config.Service) Option {
	return func(o *options) { o.config = svc }
}

// WithLogger replaces the logger handlers receive through
// [telemetry.LoggerFrom].
func WithLogger(l telemetry.Logger) Option {
	return func(o *options) { o.logger = l }
}

// New creates an application.
func New(opts ...Option) *App {
	o := options{
		guards: make(map[string]Guard),
		// What a blueprint that states no route key resolves to, so the tags
		// built here match the runtime's for a default API.
		webSocketRouteKey: DefaultWebSocketRouteKey,
	}
	for _, opt := range opts {
		opt(&o)
	}

	app := &App{registry: NewRegistry(), options: o}

	// What the deployment described, unless a service was supplied. Built here
	// rather than on first use so that a deployment asking for a store this
	// binary has no provider for is reported with the registration errors,
	// which Run prints together, rather than on whichever event first read a
	// value.
	if app.options.config == nil {
		svc, err := config.FromEnvironment()
		if err != nil {
			app.errs = append(app.errs, err)
			svc = config.New()
		}
		app.options.config = svc
	}
	return app
}

// Config returns the application's configuration.
//
// This is never nil, an application whose deployment described no store gets a service
// with no namespaces, so a provider reading it is told the application declares
// no celerity/config resource rather than having to guard against nothing.
//
// Read by resource providers resolving a blueprint name to the identifier a
// deployment recorded. A handler is given the service as an argument instead.
func (a *App) Config() *config.Service { return a.options.config }

// Registry returns the handlers registered so far.
//
// Serverless adapters and the manifest emitter read the application through it.
func (a *App) Registry() *Registry { return a.registry }

// Adapter returns an adapter given explicitly, or nil when the linked one is
// to be used.
func (a *App) Adapter() serverless.Adapter { return a.options.adapter }

// Logger returns the application's logger.
func (a *App) Logger() telemetry.Logger { return a.options.logger }

// Err returns every error collected during registration, joined.
func (a *App) Err() error { return errors.Join(a.errs...) }

func (a *App) register(reg *Registration, layers []layer.Layer) {
	reg.Layers = append(append([]layer.Layer{}, a.options.layers...), layers...)
	if err := a.registry.Add(reg); err != nil {
		a.errs = append(a.errs, err)
	}
}

// callerLocation reports where a registration was made, which is the call site
// of the registration function rather than the handler's own definition: it is
// where the route, tag and layers are decided, and so what a duplicate-tag error
// should point at.
func callerLocation(skip int) (string, int) {
	_, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return "", 0
	}
	return file, line
}
