package celeritytest

import (
	"net/url"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Matches a concrete path against a route in the router's own form, answering
// the parameters it bound.
//
// Routing is the runtime's in a deployment, so this exists only to let a test
// name the path a client would send rather than the template a handler was
// registered under. It implements what [handler.Request] documents: a segment
// parameter binds one segment, a catch-all binds one value per segment, and a
// segment is split from the raw path before it is decoded, so an encoded
// separator inside one survives.
func matchRoute(route, path string) (handler.Params, bool) {
	routeParts := split(route)
	pathParts := split(path)

	params := handler.Params{}
	for i, part := range routeParts {
		name, catchAll, isParam := parseSegment(part)
		switch {
		case isParam && catchAll:
			// The last segment of a route, binding everything that is left,
			// which may be nothing.
			for _, rest := range pathParts[min(i, len(pathParts)):] {
				params.Add(name, decode(rest))
			}
			return params, true
		case i >= len(pathParts):
			return nil, false
		case isParam:
			params.Add(name, decode(pathParts[i]))
		case part != pathParts[i]:
			return nil, false
		}
	}

	if len(pathParts) != len(routeParts) {
		return nil, false
	}

	return params, true
}

// parseSegment reads a route segment, reporting the parameter it binds.
func parseSegment(part string) (name string, catchAll, isParam bool) {
	if !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
		return "", false, false
	}
	name = part[1 : len(part)-1]
	if after, found := strings.CutPrefix(name, "*"); found {
		return after, true, true
	}
	return name, false, true
}

// split drops the leading and trailing slashes a path may or may not carry, so
// that "/orders" and "orders/" are the one path they describe.
func split(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// decode undoes percent-encoding, leaving a segment that will not decode as it
// arrived rather than failing: what a handler does with it is the handler's.
func decode(segment string) string {
	decoded, err := url.PathUnescape(segment)
	if err != nil {
		return segment
	}
	return decoded
}
