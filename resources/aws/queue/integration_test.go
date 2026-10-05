//go:build integration

// The SQS client against the service it calls, with a real AWS account
// or an emulator.
//
// What a unit test mock cannot establish: that a batch send is shaped the way SQS
// expects, and that the order a batch is answered in is restored.
//
// Run with: bash scripts/run-tests.sh --with-integration
package queue_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsresources "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

type QueueIntegrationTestSuite struct {
	suite.Suite

	target   awstest.Target
	provider *awsresources.Provider
	sqs      *sqs.Client
	queueURL string
}

func TestQueueIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(QueueIntegrationTestSuite))
}

// SetupSuite points the SDK at the target and creates the queue a deployment
// would have created, and nothing else.
func (s *QueueIntegrationTestSuite) SetupSuite() {
	var cfg aws.Config
	s.target, cfg = awstest.AWS(s.T())
	s.sqs = sqs.NewFromConfig(cfg)

	s.target.Reachable(s.T(), func(ctx context.Context) error {
		_, err := s.sqs.ListQueues(ctx, &sqs.ListQueuesInput{})
		return err
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	created, err := s.sqs.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(s.target.Name("orders-integration")),
	})
	s.Require().NoError(err)
	s.queueURL = aws.ToString(created.QueueUrl)

	s.provider = awsresources.New()
}

func (s *QueueIntegrationTestSuite) Test_messages_are_sent_to_a_queue_one_at_a_time_and_in_a_batch() {
	ctx := context.Background()
	orders, err := s.provider.Queue(awstest.SimpleRef(resources.KindQueue, "ordersQueue", s.queueURL))
	s.Require().NoError(err)

	id, err := orders.Send(ctx, []byte(`{"id":1}`))
	s.Require().NoError(err)
	s.NotEmpty(id)

	// More than one request's worth, which is where the chunking and the
	// matching of answers back to entries are decided.
	entries := make([]queue.BatchEntry, 12)
	for i := range entries {
		entries[i] = queue.BatchEntry{
			ID:   strconv.Itoa(i),
			Body: []byte(strings.Repeat("x", i+1)),
		}
	}
	result, err := orders.SendBatch(ctx, entries)
	s.Require().NoError(err)
	s.Require().Len(result.Successful, 12)
	s.Empty(result.Failed)

	var ids []string
	names := make([]string, 0, len(result.Successful))
	for _, sent := range result.Successful {
		s.NotEmpty(sent.MessageID, "entry %q should have come back with an id", sent.ID)
		ids = append(ids, sent.MessageID)
		names = append(names, sent.ID)
	}
	s.Len(unique(ids), 12, "every message should have its own id")
	s.ElementsMatch([]string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}, names,
		"every entry should be accounted for by the name it was given")

	received := s.receive(ctx, 13)
	s.Len(received, 13, "everything sent should be on the queue")
}

func (s *QueueIntegrationTestSuite) receive(ctx context.Context, want int) []string {
	var bodies []string
	deadline := time.Now().Add(20 * time.Second)

	for len(bodies) < want && time.Now().Before(deadline) {
		out, err := s.sqs.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(s.queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		s.Require().NoError(err)
		for _, message := range out.Messages {
			bodies = append(bodies, aws.ToString(message.Body))
			_, err := s.sqs.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(s.queueURL),
				ReceiptHandle: message.ReceiptHandle,
			})
			s.Require().NoError(err)
		}
	}
	return bodies
}

func unique(values []string) map[string]struct{} {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return seen
}
