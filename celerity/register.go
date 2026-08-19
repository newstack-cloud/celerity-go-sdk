package celerity

import (
	"context"
	"reflect"
	"runtime"

	"github.com/newstack-cloud/celerity-go-sdk/guard"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Guard decides whether an event may reach its handler.
type Guard = guard.Guard

// RegisterOption configures one registration.
type RegisterOption func(*registerOptions)

type registerOptions struct {
	name          string
	publishedName string
	layers        []layer.Layer
	guards        []string
	uses          []string
	public        bool
}

// Named sets the blueprint resource name for a handler.
//
// Without it the name is derived from the handler function's own name, which is
// what a blueprint generated from the manifest will use.
func Named(name string) RegisterOption {
	return func(o *registerOptions) { o.name = name }
}

// PublishedAs sets the name a deployment addresses this handler by, which is
// the blueprint's spec.handlerName.
func PublishedAs(name string) RegisterOption {
	return func(o *registerOptions) { o.publishedName = name }
}

// With adds handler-scoped layers, applied inside any application layers.
func With(layers ...layer.Layer) RegisterOption {
	return func(o *registerOptions) { o.layers = append(o.layers, layers...) }
}

// ProtectedBy names the guards that must allow an event before it reaches this
// handler.
func ProtectedBy(guards ...string) RegisterOption {
	return func(o *registerOptions) { o.guards = append(o.guards, guards...) }
}

// Public marks a handler as reachable without authorisation, overriding an
// application-wide guard.
func Public() RegisterOption {
	return func(o *registerOptions) { o.public = true }
}

// Uses declares blueprint resources this handler reaches, so the deployment
// grants it access to them.
//
// Extraction finds resources through the reference graph on its own, and Uses
// is additive to that. It is for what analysis cannot see: a resource named by
// a value that is not a compile-time constant, or one reached by a mechanism
// the reference graph does not model.
//
// There is no option to narrow. Removing a reference the walk found would let a
// handler call something it has no permission for, failing at runtime rather
// than at build time.
func Uses(resourceNames ...string) RegisterOption {
	return func(o *registerOptions) { o.uses = append(o.uses, resourceNames...) }
}

// HandlerFunc is a typed handler: the framework decodes the request into In and
// encodes Out into the response, so a handler deals only in its own types.
type HandlerFunc[In, Out any] func(ctx context.Context, in In) (Out, error)

// Get registers a handler for GET requests to route.
func Get[In, Out any](app *App, route string, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerHTTP(app, "GET", route, h, opts)
}

// Post registers a handler for POST requests to route.
func Post[In, Out any](app *App, route string, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerHTTP(app, "POST", route, h, opts)
}

// Put registers a handler for PUT requests to route.
func Put[In, Out any](app *App, route string, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerHTTP(app, "PUT", route, h, opts)
}

// Patch registers a handler for PATCH requests to route.
func Patch[In, Out any](app *App, route string, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerHTTP(app, "PATCH", route, h, opts)
}

// Delete registers a handler for DELETE requests to route.
func Delete[In, Out any](app *App, route string, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerHTTP(app, "DELETE", route, h, opts)
}

// HTTP registers a handler that works in the wire vocabulary directly, for the
// cases where decoding into a typed request is not what is wanted.
func HTTP(
	app *App,
	method, route string,
	h func(context.Context, *handler.Request) (*handler.Response, error),
	opts ...RegisterOption,
) {
	o := applyOptions(opts)
	file, line := callerLocation(1)
	normalised := NormaliseRoute(route)

	app.register(&Registration{
		Tag:           HTTPTag(method, normalised),
		Name:          handlerName(o.name, h),
		PublishedName: o.publishedName,
		Kind:          handler.KindHTTP,
		Method:        method,
		Route:         normalised,
		SourceFile:    file,
		SourceLine:    line,
		Handler:       wrapHTTP(h),
		Guards:        o.guards,
		Uses:          o.uses,
		Public:        o.public,
		FuncPath:      funcNameOf(h),
	}, o.layers)
}

// OnMessage registers a handler for WebSocket messages arriving on routeKey.
func OnMessage[In, Out any](
	app *App,
	routeKey string,
	h HandlerFunc[In, Out],
	opts ...RegisterOption,
) {
	registerWebSocket(app, routeKey, "$default", h, opts)
}

// OnConnect registers a handler run when a WebSocket client connects.
func OnConnect[In, Out any](app *App, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerWebSocket(app, "$connect", "$connect", h, opts)
}

// OnDisconnect registers a handler run when a WebSocket client disconnects.
func OnDisconnect[In, Out any](app *App, h HandlerFunc[In, Out], opts ...RegisterOption) {
	registerWebSocket(app, "$disconnect", "$disconnect", h, opts)
}

// Consume registers a handler for batches from a queue, stream or event source.
//
// The handler is given the whole batch and returns a [handler.BatchResult], so
// that one failing record redrives that record rather than the batch.
func Consume(
	app *App,
	sourceID string,
	h func(context.Context, *handler.ConsumerBatch) (*handler.BatchResult, error),
	opts ...RegisterOption,
) {
	o := applyOptions(opts)
	file, line := callerLocation(1)
	name := handlerName(o.name, h)

	app.register(&Registration{
		Tag:           SourceTag(sourceID, name),
		Name:          name,
		PublishedName: o.publishedName,
		Kind:          handler.KindConsumer,
		SourceID:      sourceID,
		SourceFile:    file,
		SourceLine:    line,
		Handler:       wrapConsumer(h),
		Guards:        o.guards,
		Uses:          o.uses,
		Public:        o.public,
		FuncPath:      funcNameOf(h),
	}, o.layers)
}

// Schedule registers a handler run when a scheduled rule fires.
func Schedule(
	app *App,
	sourceID string,
	h func(context.Context, *handler.ScheduleTrigger) error,
	opts ...RegisterOption,
) {
	o := applyOptions(opts)
	file, line := callerLocation(1)
	name := handlerName(o.name, h)

	app.register(&Registration{
		Tag:           SourceTag(sourceID, name),
		Name:          name,
		PublishedName: o.publishedName,
		Kind:          handler.KindSchedule,
		SourceID:      sourceID,
		SourceFile:    file,
		SourceLine:    line,
		Handler:       wrapSchedule(h),
		Guards:        o.guards,
		Uses:          o.uses,
		Public:        o.public,
		FuncPath:      funcNameOf(h),
	}, o.layers)
}

// Invoke registers a handler reachable by name, from another handler or from
// the runtime's local invoke endpoint.
func Invoke[In, Out any](
	app *App,
	name string,
	h HandlerFunc[In, Out],
	opts ...RegisterOption,
) {
	o := applyOptions(opts)
	file, line := callerLocation(1)

	app.register(&Registration{
		Tag:           CustomTag(name),
		Name:          name,
		PublishedName: o.publishedName,
		Kind:          handler.KindCustom,
		SourceFile:    file,
		SourceLine:    line,
		Handler:       wrapCustom(h),
		Guards:        o.guards,
		Uses:          o.uses,
		Public:        o.public,
		FuncPath:      funcNameOf(h),
	}, o.layers)
}

// AddGuard registers a named guard that handlers refer to through [ProtectedBy].
func AddGuard(app *App, name string, g Guard) {
	if _, exists := app.options.guards[name]; exists {
		file, line := callerLocation(1)
		app.errs = append(app.errs, guardRedefinedError(name, file, line))
		return
	}
	app.options.guards[name] = g
}

func registerHTTP[In, Out any](
	app *App,
	method, route string,
	h HandlerFunc[In, Out],
	opts []RegisterOption,
) {
	o := applyOptions(opts)
	file, line := callerLocation(2)
	normalised := NormaliseRoute(route)

	app.register(&Registration{
		Tag:           HTTPTag(method, normalised),
		Name:          handlerName(o.name, h),
		PublishedName: o.publishedName,
		Kind:          handler.KindHTTP,
		Method:        method,
		Route:         normalised,
		SourceFile:    file,
		SourceLine:    line,
		Handler:       wrapTypedHTTP(h),
		Guards:        o.guards,
		Uses:          o.uses,
		Public:        o.public,
		FuncPath:      funcNameOf(h),
	}, o.layers)
}

func registerWebSocket[In, Out any](
	app *App,
	routeKey, route string,
	h HandlerFunc[In, Out],
	opts []RegisterOption,
) {
	o := applyOptions(opts)
	file, line := callerLocation(2)

	app.register(&Registration{
		Tag:           WebSocketTag(routeKey, route),
		Name:          handlerName(o.name, h),
		PublishedName: o.publishedName,
		Kind:          handler.KindWebSocket,
		RouteKey:      routeKey,
		Route:         route,
		SourceFile:    file,
		SourceLine:    line,
		Handler:       wrapTypedWebSocket(h),
		Guards:        o.guards,
		Uses:          o.uses,
		Public:        o.public,
		FuncPath:      funcNameOf(h),
	}, o.layers)
}

func applyOptions(opts []RegisterOption) registerOptions {
	var o registerOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// handlerName falls back to the handler function's own name, which is also what
// anchors the static extraction pass to the function it should analyse.
func handlerName(explicit string, h any) string {
	if explicit != "" {
		return explicit
	}
	return shortFuncName(funcNameOf(h))
}

func funcNameOf(h any) string {
	v := reflect.ValueOf(h)
	if v.Kind() != reflect.Func {
		return ""
	}
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return ""
	}
	return fn.Name()
}
