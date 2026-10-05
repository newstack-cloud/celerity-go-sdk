package queue

import (
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

// Queues returns a builder driving the given API, so that a suite exercises the
// real client against a stand-in rather than against SQS.
func Queues(api API) func(resources.Ref) (queue.Client, error) {
	return (&queues{api: api}).build
}
