package queue

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

// Linking this package is what lets an application reach a queue on AWS.
func init() {
	service.RegisterQueue(func(s *service.Session) service.Builder[queue.Client] {
		return newQueues(s).build
	})
}

// API is what this package calls on SQS. Narrow on purpose: it is also the list
// of what a handler reaching a queue needs permission to do.
type API interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
	SendMessageBatch(context.Context, *sqs.SendMessageBatchInput, ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error)
}

type queues struct {
	session *service.Session
	clients service.Clients[API]
	api     API
}

func newQueues(s *service.Session) *queues {
	return &queues{session: s}
}

func (q *queues) build(ref resources.Ref) (queue.Client, error) {
	return &sqsQueue{queues: q, ref: ref}, nil
}

func (q *queues) sqs(ctx context.Context, key service.ClientKey) (API, error) {
	if q.api != nil {
		return q.api, nil
	}

	return q.clients.Get(ctx, key, func() (API, error) {
		cfg, err := q.session.ConfigFor(ctx, key.Region)
		if err != nil {
			return nil, err
		}
		return sqs.NewFromConfig(cfg), nil
	})
}
