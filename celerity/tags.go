package celerity

import (
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Handler tags are how the Celerity runtime addresses a handler. The formats
// below mirror the runtime's own construction exactly: a tag that differs by a
// byte fails the startup handshake rather than misrouting later.

// HTTPTag returns the tag identifying an HTTP handler.
//
// Route must already be in the router's form, which [NormaliseRoute] produces.
func HTTPTag(method, route string) string {
	return strings.ToUpper(method) + "::" + route
}

// DefaultWebSocketRouteKey is the field a WebSocket message carries its route
// in when the API does not state one.
//
// It matches the runtime's DEFAULT_WEBSOCKET_API_ROUTE_KEY, which is what a
// blueprint omitting spec.websocketConfig.routeKey resolves to, so a handler
// registered against a default API declares the tag the runtime builds.
const DefaultWebSocketRouteKey = "event"

// WebSocketTag returns the tag identifying a WebSocket message handler.
//
// The route key names the field carrying the route, and the route is its value,
// so {"action": "sendMessage"} on an API keyed by "action" is
// action::sendMessage. Both halves come from the API rather than the handler,
// which is why registration takes only the route.
func WebSocketTag(routeKey, route string) string {
	return routeKey + "::" + route
}

// SourceTag returns the tag identifying a consumer or schedule handler, keyed by
// the source it is bound to.
func SourceTag(sourceID, handlerName string) string {
	return "source::" + sourceID + "::" + handlerName
}

// CustomTag returns the tag identifying a custom handler.
func CustomTag(handlerName string) string {
	return "custom::" + handlerName
}

// NormaliseRoute converts a blueprint route template into the router's form,
// which is what handler tags and [handler.Request.Route] are built from.
//
//	/files/{path+}  ->  /files/{*path}
//
// It is [handler.NormaliseRoute]: the same translation a serverless adapter
// applies when it maps a route the platform states in the blueprint's form, so
// there is one definition of the router's spelling rather than one per mapper.
func NormaliseRoute(route string) string {
	return handler.NormaliseRoute(route)
}
