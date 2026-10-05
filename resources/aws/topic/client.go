package topic

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Linking this package is what lets an application reach a topic on AWS.
func init() {
	service.RegisterTopic(func(s *service.Session) service.Builder[topic.Client] {
		return newTopics(s).build
	})
}

// API is what this package calls on SNS. Narrow on purpose: it is also the list
// of what a handler reaching a topic needs permission to do.
type API interface {
	Publish(context.Context, *sns.PublishInput, ...func(*sns.Options)) (*sns.PublishOutput, error)
	PublishBatch(
		context.Context, *sns.PublishBatchInput, ...func(*sns.Options),
	) (*sns.PublishBatchOutput, error)
}

type topics struct {
	session *service.Session
	clients service.Clients[API]
	api     API
}

func newTopics(s *service.Session) *topics { return &topics{session: s} }

func (t *topics) build(ref resources.Ref) (topic.Client, error) {
	return &snsTopic{topics: t, ref: ref}, nil
}

func (t *topics) sns(ctx context.Context, key service.ClientKey) (API, error) {
	if t.api != nil {
		return t.api, nil
	}

	return t.clients.Get(ctx, key, func() (API, error) {
		cfg, err := t.session.ConfigFor(ctx, key.Region)
		if err != nil {
			return nil, err
		}
		return sns.NewFromConfig(cfg), nil
	})
}
