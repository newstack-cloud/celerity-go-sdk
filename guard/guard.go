// Package guard provides Celerity's authorisation model.
//
// A guard is a layer that returns a decision rather than a result, so a denial
// is a 401 or a 403 the framework writes rather than an error every handler has
// to remember to shape the same way.
package guard

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Request is what a guard decides about.
//
// It carries the parts of an event that authorisation is decided from, so one
// guard can protect handlers of different kinds.
type Request struct {
	Kind    handler.Kind
	Method  string
	Route   string
	Headers handler.Params
	Query   handler.Params
	// ConnectionID is set for WebSocket events.
	ConnectionID string
	SourceIP     string
	RequestID    string
}

// Decision is a guard's answer.
type Decision struct {
	// Allowed reports whether the event may reach the handler.
	Allowed bool
	// Reason is reported to the caller when the event is refused, and should say
	// what is missing without saying what would satisfy it.
	Reason string
	// Identity is the authenticated principal, reached from a handler through
	// [IdentityFrom]. Nil when the guard authorises without identifying.
	Identity any
	// Claims are the verified token claims, when the guard verified a token.
	Claims map[string]any
}

// Allow returns a decision permitting the event, carrying an identity.
func Allow(identity any) Decision {
	return Decision{Allowed: true, Identity: identity}
}

// Deny returns a decision refusing the event.
func Deny(reason string) Decision {
	return Decision{Allowed: false, Reason: reason}
}

// Guard decides whether an event may reach its handler.
//
// Returning an error means the decision could not be made, which is a 500,
// as distinct from a decision to refuse, which is a 401 or 403.
type Guard func(ctx context.Context, req *Request) (Decision, error)

type identityKey struct{}

// WithIdentity returns a context carrying the identity a guard established.
func WithIdentity(ctx context.Context, decision Decision) context.Context {
	return context.WithValue(ctx, identityKey{}, decision)
}

// IdentityFrom returns the identity the guard protecting this handler
// established, and whether there was one.
func IdentityFrom(ctx context.Context) (Decision, bool) {
	d, ok := ctx.Value(identityKey{}).(Decision)
	return d, ok
}
