package celerity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// BlueprintHandler is one handler the blueprint declares, as the runtime
// reports it before the handshake.
//
// It is this package's own type rather than the protocol's, so reconciliation
// can be described and tested without the wire contract.
type BlueprintHandler struct {
	// Name is the blueprint resource name.
	Name string
	// PublishedName is the name the blueprint publishes the handler under, from
	// spec.handlerName. Empty where it sets none.
	PublishedName string
	// Tag is what the runtime addresses the handler by.
	Tag string
}

// UnresolvedHandlerError reports handlers that take their wiring from the
// blueprint and were not found in it.
type UnresolvedHandlerError struct {
	// Names are registered in code and absent from the blueprint.
	Names []string
	// Declared are the names the blueprint does declare, which is what a typo
	// is most easily spotted against.
	Declared []string
}

func (e *UnresolvedHandlerError) Error() string {
	return fmt.Sprintf(
		"these handlers take their wiring from the blueprint and are not declared in it: %s. "+
			"The blueprint declares: %s",
		strings.Join(e.Names, ", "), strings.Join(e.Declared, ", "),
	)
}

// ReconcileTags settles every tag that code could not state on its own, and
// returns the tags this process serves.
//
// Two kinds of handler need it, for opposite reasons.
//
// A handler registered with [Handler] states only its name where its route, method
// or source is the blueprint's to decide, so its tag is whatever the blueprint
// gives the handler of that name. One that the blueprint does not declare is an
// error rather than a handler with no tag, since it could never be dispatched
// to.
//
// A WebSocket handler registered with [OnMessage] states its route, matching
// the other language SDKs where a decorator carries it and extraction writes it into the
// blueprint. The route key belongs to the API, so it is taken from whatever the
// blueprint keys that route by. One left unmatched keeps the tag built from
// [WithWebSocketRouteKey], so the handshake reports the tag the handler
// actually declared rather than hiding a mismatch behind a guess.
//
// The runtime sends what the blueprint declares before this process declares
// itself, which is the only window in which both are known.
func (a *App) ReconcileTags(blueprint []BlueprintHandler) ([]string, error) {
	byRoute := webSocketTagsByRoute(blueprint)
	byName := make(map[string]BlueprintHandler, len(blueprint))
	for _, declared := range blueprint {
		byName[declared.Name] = declared
		if declared.PublishedName != "" {
			byName[declared.PublishedName] = declared
		}
	}

	var unresolved []string
	for _, reg := range a.registry.All() {
		switch {
		case reg.FromBlueprint:
			declared, ok := byName[reg.Name]
			if !ok {
				unresolved = append(unresolved, reg.Name)
				continue
			}
			a.bind(reg, declared.Tag)

		case reg.Kind == handler.KindWebSocket:
			if declared, ok := byRoute[reg.Route]; ok {
				a.bind(reg, declared)
				reg.RouteKey = routeKeyOf(declared)
			}
		}
	}

	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		return nil, &UnresolvedHandlerError{
			Names:    unresolved,
			Declared: namesOf(blueprint),
		}
	}

	return a.registry.Tags(), nil
}

// Moves a registration onto the tag the blueprint declares, working out what it
// serves from the shape of that tag where code did not say.
func (a *App) bind(reg *Registration, tag string) {
	if reg.Kind == "" {
		reg.Kind = kindOfTag(tag)
	}
	if tag == reg.Tag {
		return
	}
	a.registry.Retag(reg.Tag, tag)
	reg.Tag = tag
}

// Reads the event source from a handler tag.
//
// The formats are distinct enough to tell apart, which is what lets a handler
// declare only its name and still be dispatched correctly:
//
//	GET::/orders                      http
//	event::sendMessage                websocket
//	source::orderQueue::processOrder  consumer or schedule
//	custom::recalculatePricing        custom
//
// A source tag cannot say which of consumer or schedule it is, and nothing here
// needs it to: both are dispatched by tag, and the event says which arrived.
func kindOfTag(tag string) handler.Kind {
	switch {
	case strings.HasPrefix(tag, "custom::"):
		return handler.KindCustom
	case strings.HasPrefix(tag, "source::"):
		return handler.KindConsumer
	}

	if _, route, found := strings.Cut(tag, "::"); found && strings.HasPrefix(route, "/") {
		return handler.KindHTTP
	}
	return handler.KindWebSocket
}

// Indexes the blueprint's tags by the route half.
//
// Only tags with exactly one separator are considered, for example, a consumer tag is
// source::{sourceId}::{handlerName}, and splitting it from the right would take
// the handler name for a route.
func webSocketTagsByRoute(blueprint []BlueprintHandler) map[string]string {
	byRoute := make(map[string]string, len(blueprint))
	for _, declared := range blueprint {
		key, route, found := strings.Cut(declared.Tag, "::")
		if !found || key == "" || strings.Contains(route, "::") {
			continue
		}
		// An HTTP tag is {METHOD}::{route}, and a route always starts with a
		// slash, so those are not WebSocket tags.
		if strings.HasPrefix(route, "/") {
			continue
		}
		byRoute[route] = declared.Tag
	}
	return byRoute
}

func routeKeyOf(tag string) string {
	key, _, _ := strings.Cut(tag, "::")
	return key
}

func namesOf(blueprint []BlueprintHandler) []string {
	names := make([]string, 0, len(blueprint))
	for _, declared := range blueprint {
		names = append(names, declared.Name)
	}
	sort.Strings(names)
	return names
}

// HandlerLimits returns the per-handler concurrency caps, keyed by tag.
//
// Read after [App.ReconcileTags] rather than before as reconciliation can change
// a WebSocket handler's tag to the one the blueprint declares, and a cap keyed
// by the tag built before that would name a handler the runtime does not know.
func (a *App) HandlerLimits() map[string]int {
	var limits map[string]int
	for _, reg := range a.registry.All() {
		if reg.MaxConcurrent <= 0 {
			continue
		}
		if limits == nil {
			limits = make(map[string]int)
		}
		limits[reg.Tag] = reg.MaxConcurrent
	}
	return limits
}
