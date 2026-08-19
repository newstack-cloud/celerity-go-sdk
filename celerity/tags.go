package celerity

import (
	"regexp"
	"strings"
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

// WebSocketTag returns the tag identifying a WebSocket message handler.
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

// A blueprint declares a catch-all path parameter as {name+}; the runtime's
// router spells the same thing {*name}. Tags are built from the router's form,
// so the translation happens once, here.
var catchAllParam = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\+\}`)

// NormaliseRoute converts a blueprint route template into the router's form,
// which is what handler tags and [handler.Request.Route] are built from.
//
//	/files/{path+}  ->  /files/{*path}
func NormaliseRoute(route string) string {
	return catchAllParam.ReplaceAllString(route, "{*$1}")
}
