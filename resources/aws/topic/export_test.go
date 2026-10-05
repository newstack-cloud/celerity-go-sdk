package topic

import (
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Topics returns a builder driving the given API, so that a suite exercises the
// real client against a stand-in rather than against SNS.
func Topics(api API) func(resources.Ref) (topic.Client, error) {
	return (&topics{api: api}).build
}
