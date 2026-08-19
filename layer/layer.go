// Package layer provides Celerity's middleware model.
//
// A layer is Go's usual middleware shape, a function that wraps the next one,
// so it composes with anything already written that way and needs no framework
// knowledge to write.
package layer

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// Next runs the remainder of the pipeline, ending in the handler itself.
type Next func(ctx context.Context, ev *handler.Event) (*handler.Result, error)

// Layer wraps the rest of the pipeline.
//
// Work before calling next happens on the way in, work after it on the way out,
// and not calling it at all short-circuits, which is how a cache layer answers
// without reaching the handler.
type Layer func(next Next) Next

// Chain composes layers into one, applying them left to right so that the first
// layer given is the outermost and the handler runs innermost.
//
// Scopes compose the same way: application layers wrap group layers, which wrap
// handler layers.
func Chain(layers ...Layer) Layer {
	return func(next Next) Next {
		for i := len(layers) - 1; i >= 0; i-- {
			if layers[i] != nil {
				next = layers[i](next)
			}
		}
		return next
	}
}

// Apply runs a handler through layers, returning the composed entry point.
func Apply(h handler.Func, layers ...Layer) Next {
	return Chain(layers...)(Next(h))
}
