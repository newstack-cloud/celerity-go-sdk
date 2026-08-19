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
	concurrency      int
	handlerLimits    map[string]int
	layers           []layer.Layer
	guards           map[string]Guard
	adapter          serverless.Adapter
	logger           telemetry.Logger
	resourceProvider resources.Provider
}

// Option configures an application.
type Option func(*options)

// WithConcurrency sets how many events may be in flight at once.
//
// It is the worker pool size, and it becomes the initial credit the IPC
// handshake declares. Throughput saturates at the pool size, and every unit
// beyond it adds latency for almost no throughput, so raising it past the pool
// is not a tuning knob worth reaching for.
func WithConcurrency(n int) Option {
	return func(o *options) { o.concurrency = n }
}

// WithHandlerLimit caps how much of the concurrency window one handler tag may
// occupy, so a slow handler cannot starve the others.
func WithHandlerLimit(tag string, max int) Option {
	return func(o *options) {
		if o.handlerLimits == nil {
			o.handlerLimits = make(map[string]int)
		}
		o.handlerLimits[tag] = max
	}
}

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

// WithLogger replaces the logger handlers receive through
// [telemetry.LoggerFrom].
func WithLogger(l telemetry.Logger) Option {
	return func(o *options) { o.logger = l }
}

// New creates an application.
func New(opts ...Option) *App {
	o := options{
		concurrency: runtime.NumCPU() * 4,
		guards:      make(map[string]Guard),
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &App{registry: NewRegistry(), options: o}
}

// Registry returns the handlers registered so far.
//
// Serverless adapters and the manifest emitter read the application through it.
func (a *App) Registry() *Registry { return a.registry }

// Concurrency returns the configured worker pool size.
func (a *App) Concurrency() int { return a.options.concurrency }

// HandlerLimits returns the per-tag concurrency caps.
func (a *App) HandlerLimits() map[string]int { return a.options.handlerLimits }

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
