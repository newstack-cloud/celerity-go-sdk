package handler

import "regexp"

// A blueprint declares a catch-all path parameter as {name+}; the router the
// Celerity runtime serves with spells the same thing {*name}. [Request.Route]
// carries the router's form, and so do the handler tags built from it, so the
// translation happens once, here.
//
// It lives in this package rather than alongside tag construction because a
// serverless adapter needs it too: API Gateway states a route in the
// blueprint's form, and a handler must see the same Route whichever side
// mapped it.
var catchAllParam = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\+\}`)

// NormaliseRoute converts a route template from the blueprint's form into the
// router's form, which is what [Request.Route] and handler tags are built from.
//
//	/files/{path+}  ->  /files/{*path}
func NormaliseRoute(route string) string {
	return catchAllParam.ReplaceAllString(route, "{*$1}")
}

// CatchAllParam returns the name of the catch-all parameter in a route
// template, in either form, and whether there is one.
//
// A route has at most one: it matches the rest of the path, so nothing can
// follow it.
func CatchAllParam(route string) (string, bool) {
	if m := catchAllParam.FindStringSubmatch(route); m != nil {
		return m[1], true
	}
	if m := routerCatchAll.FindStringSubmatch(route); m != nil {
		return m[1], true
	}
	return "", false
}

var routerCatchAll = regexp.MustCompile(`\{\*([A-Za-z_][A-Za-z0-9_]*)\}`)
