// Package backend is the seam between the local provider and what actually
// serves a resource in a development session.
//
// The provider knows which resource kinds a session has to serve itself and
// which it hands to the platform's provider. What serves them is a separate
// decision: today a queue and a topic are Redis, because that is what the
// runtime's consumer side reads locally, and nothing about the provider assumes
// it. A backend package registers what it can build when it is linked, and the
// provider asks here rather than importing it.
//
// The same arrangement as the AWS module's internal/service, for the same two
// reasons: a session whose blueprint declares no queue does not link a Redis
// client for one, and a backend that is not Redis can be added without the
// provider learning about it.
package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Builder makes a client for one resource, named by the reference a handle
// carries.
type Builder[T any] func(resources.Ref) (T, error)

// One slot per kind rather than a map of any, so that the provider's methods
// stay typed and a registration that does not match is a compile error.
var (
	queues    Builder[queue.Client]
	topics    Builder[topic.Client]
	releasers []func(context.Context) error
)

// RegisterQueue is called by a backend package that can serve a local queue.
func RegisterQueue(b Builder[queue.Client]) {
	queues = b
}

// RegisterTopic is called by a backend package that can serve a local topic.
func RegisterTopic(b Builder[topic.Client]) {
	topics = b
}

// RegisterReleaser is called by a backend package that holds a connection worth
// giving back when the session stops.
func RegisterReleaser(release func(context.Context) error) {
	releasers = append(releasers, release)
}

// Queues returns the builder a backend registered.
func Queues() (Builder[queue.Client], error) {
	if queues == nil {
		return nil, missing(resources.KindQueue)
	}
	return queues, nil
}

// Topics returns the builder a backend registered.
func Topics() (Builder[topic.Client], error) {
	if topics == nil {
		return nil, missing(resources.KindTopic)
	}
	return topics, nil
}

// Release gives back what the linked backends hold.
//
// Every releaser is called even when an earlier one fails: shutdown is the one
// time carrying on matters more than the error.
func Release(ctx context.Context) error {
	var errs []error
	for _, release := range releasers {
		if err := release(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// missing names the import a build left out, which is the only way this
// happens: the provider is linked and the backend for one kind is not.
func missing(kind resources.Kind) error {
	return fmt.Errorf(
		"celerity: a development session has no backend linked for a %s. The generated "+
			"platform file should import one, for example:\n\n"+
			"\timport _ \"github.com/newstack-cloud/celerity-go-sdk/resources/local/redis\"",
		kind)
}
