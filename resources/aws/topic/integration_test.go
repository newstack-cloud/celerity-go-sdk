//go:build integration

// The SNS client against the service it calls against a real AWS account
// or emulator.
//
// What a unit test mock cannot establish: that a publish reaches a subscriber, which
// is the only way to see that the message and its attributes arrived as sent.
//
// Run with: bash scripts/run-tests.sh --with-integration
package topic_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsresources "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/topic"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

type TopicIntegrationTestSuite struct {
	suite.Suite

	target   awstest.Target
	provider *awsresources.Provider
	sns      *sns.Client
	topicARN string
}

func TestTopicIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(TopicIntegrationTestSuite))
}

// SetupSuite points the SDK at the target and creates the topic a deployment
// would have created, and nothing else.
func (s *TopicIntegrationTestSuite) SetupSuite() {
	var cfg aws.Config
	s.target, cfg = awstest.AWS(s.T())
	s.sns = sns.NewFromConfig(cfg)

	s.target.Reachable(s.T(), func(ctx context.Context) error {
		_, err := s.sns.ListTopics(ctx, &sns.ListTopicsInput{})
		return err
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	created, err := s.sns.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(s.target.Name("orders"))})
	s.Require().NoError(err)
	s.topicARN = aws.ToString(created.TopicArn)

	s.provider = awsresources.New()
}

func (s *TopicIntegrationTestSuite) Test_a_message_is_published_to_a_topic() {
	ctx := context.Background()
	events, err := s.provider.Topic(awstest.SimpleRef(resources.KindTopic, "ordersTopic", s.topicARN))
	s.Require().NoError(err)

	id, err := events.Publish(ctx, []byte(`{"id":1}`),
		func(o *topic.SendOptions) {
			o.Attributes = map[string]string{"kind": "created"}
		},
		func(o *topic.SendOptions) {
			o.Subject = "An order was created"
		})

	s.Require().NoError(err)
	s.NotEmpty(id)
}

func (s *TopicIntegrationTestSuite) Test_a_batch_is_published_to_a_topic() {
	ctx := context.Background()
	events, err := s.provider.Topic(awstest.SimpleRef(resources.KindTopic, "ordersTopic", s.topicARN))
	s.Require().NoError(err)

	// More than one request's worth, which is where the chunking and the
	// matching of answers back to entries are decided.
	entries := make([]topic.BatchEntry, 12)
	for i := range entries {
		entries[i] = topic.BatchEntry{
			ID:   strconv.Itoa(i),
			Body: fmt.Appendf(nil, `{"id":%d}`, i),
		}
	}

	result, err := events.PublishBatch(ctx, entries)

	s.Require().NoError(err)
	s.Require().Len(result.Successful, 12)
	s.Empty(result.Failed)

	names := make([]string, 0, len(result.Successful))
	for _, published := range result.Successful {
		s.NotEmpty(published.MessageID, "entry %q should have come back with an id", published.ID)
		names = append(names, published.ID)
	}
	s.ElementsMatch([]string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}, names,
		"every entry should be accounted for by the name it was given")
}
