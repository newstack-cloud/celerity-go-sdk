package celerity

import (
	"fmt"
	"sort"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Registration is one handler as it was registered, along with everything the
// framework needs in order to run it and everything the CLI needs in order to
// describe it.
type Registration struct {
	// Tag addresses this handler in the runtime, built by [HTTPTag] and friends.
	Tag string
	// Name is the blueprint resource name this handler is declared as.
	Name string
	// PublishedName is the name the blueprint publishes the handler under, from
	// spec.handlerName. Empty when the blueprint does not set one.
	PublishedName string
	Kind          handler.Kind

	// Method and Route are set for HTTP handlers, Route in the router's form.
	Method string
	Route  string
	// RouteKey is set for WebSocket handlers.
	RouteKey string
	// SourceID is set for consumer and schedule handlers.
	SourceID string

	// SourceFile and SourceLine are the location of the registration call,
	// captured for error messages and for the handler manifest.
	SourceFile string
	SourceLine int
	// FuncPath is the fully qualified name of the handler function, which roots
	// the extraction tool's static resource walk.
	FuncPath string

	Handler handler.Func
	Layers  []layer.Layer
	Guards  []string
	// Uses names resources declared explicitly through [Uses], additive to what
	// static extraction finds.
	Uses []string
	// Public marks a handler reachable without authorisation.
	Public bool
	// MaxConcurrent caps how many events of this handler may be in flight,
	// zero meaning uncapped.
	MaxConcurrent int
	// FromBlueprint marks a handler that stated only its name, leaving its
	// route, method or source to the blueprint. Its tag is settled by
	// [App.ReconcileTags] rather than at registration.
	FromBlueprint bool
}

// Registry holds every handler an application has registered, keyed by tag.
//
// It is the single source of truth for three things that must agree: what the
// dispatcher routes against, what the handshake declares to the runtime, and
// what the handler manifest reports to the CLI.
type Registry struct {
	mu    sync.RWMutex
	byTag map[string]*Registration
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byTag: make(map[string]*Registration)}
}

// Add registers a handler.
//
// Registering two handlers under one tag is a programming error rather than a
// last-write-wins: the runtime addresses handlers by tag, so the second would
// silently take every event meant for the first.
func (r *Registry) Add(reg *Registration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.byTag[reg.Tag]; ok {
		return fmt.Errorf(
			"handler %q is already registered at %s:%d, and %s:%d registers it again",
			reg.Tag, existing.SourceFile, existing.SourceLine, reg.SourceFile, reg.SourceLine,
		)
	}

	r.byTag[reg.Tag] = reg
	r.order = append(r.order, reg.Tag)
	return nil
}

// Retag moves a registration to a different tag, keeping registration order.
//
// Used when the blueprint declares a WebSocket route key other than the one a
// tag was built with, which is not known until the runtime sends its
// configuration.
func (r *Registry) Retag(from, to string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	reg, ok := r.byTag[from]
	if !ok || from == to {
		return
	}

	delete(r.byTag, from)
	r.byTag[to] = reg
	for i, tag := range r.order {
		if tag == from {
			r.order[i] = to
			break
		}
	}
}

// Get returns the handler registered under tag.
func (r *Registry) Get(tag string) (*Registration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.byTag[tag]
	return reg, ok
}

// ByName returns the handler whose blueprint resource name or published name is
// name. Serverless adapters resolve CELERITY_HANDLER_ID through this.
func (r *Registry) ByName(name string) (*Registration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, tag := range r.order {
		reg := r.byTag[tag]
		if reg.Name == name || reg.PublishedName == name {
			return reg, true
		}
	}
	return nil, false
}

// All returns every registration in registration order.
func (r *Registry) All() []*Registration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]*Registration, 0, len(r.order))
	for _, tag := range r.order {
		all = append(all, r.byTag[tag])
	}
	return all
}

// OfKind returns every registration for one event source.
func (r *Registry) OfKind(kind handler.Kind) []*Registration {
	var matched []*Registration
	for _, reg := range r.All() {
		if reg.Kind == kind {
			matched = append(matched, reg)
		}
	}
	return matched
}

// Tags returns every registered tag, sorted, which is what the IPC handshake
// declares to the runtime.
func (r *Registry) Tags() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tags := make([]string, 0, len(r.byTag))
	for tag := range r.byTag {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

// Len returns the number of registered handlers.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byTag)
}
