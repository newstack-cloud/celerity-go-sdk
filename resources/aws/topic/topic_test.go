package topic_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awstopic "github.com/newstack-cloud/celerity-go-sdk/resources/aws/topic"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

type TopicTestSuite struct {
	suite.Suite
}

func TestTopicTestSuite(t *testing.T) {
	suite.Run(t, new(TopicTestSuite))
}

const ordersTopicARN = "arn:aws:sns:eu-west-2:1:orders"

type fakeSNS struct {
	published *sns.PublishInput
	batches   []*sns.PublishBatchInput

	// answer decides what a batch comes back as, so that the entry ordering
	// SNS does not promise can be reproduced.
	answer func(*sns.PublishBatchInput) *sns.PublishBatchOutput
	err    error

	// failFrom is the batch request, counted from one, that fails and every one
	// after it, so that a batch published over several requests can fail part
	// way.
	failFrom int
}

func (f *fakeSNS) Publish(
	_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options),
) (*sns.PublishOutput, error) {
	f.published = in
	if f.err != nil {
		return nil, f.err
	}
	return &sns.PublishOutput{MessageId: aws.String("message-1")}, nil
}

func (f *fakeSNS) PublishBatch(
	_ context.Context, in *sns.PublishBatchInput, _ ...func(*sns.Options),
) (*sns.PublishBatchOutput, error) {
	f.batches = append(f.batches, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.failFrom > 0 && len(f.batches) >= f.failFrom {
		return nil, errors.New("Throttled")
	}
	if f.answer != nil {
		return f.answer(in), nil
	}
	return acceptAll(in), nil
}

// acceptAll answers the way SNS does when every entry was taken, in an order
// that is not the order they were published in.
func acceptAll(in *sns.PublishBatchInput) *sns.PublishBatchOutput {
	out := &sns.PublishBatchOutput{}
	for i := len(in.PublishBatchRequestEntries) - 1; i >= 0; i-- {
		id := in.PublishBatchRequestEntries[i].Id
		out.Successful = append(out.Successful, snstypes.PublishBatchResultEntry{
			Id:        id,
			MessageId: aws.String("message-" + aws.ToString(id)),
		})
	}
	return out
}

func (s *TopicTestSuite) topic(api *fakeSNS) topic.Client {
	t, err := awstopic.Topics(api)(awstest.SimpleRef(resources.KindTopic, "ordersTopic", ordersTopicARN))
	s.Require().NoError(err)
	return t
}

func (s *TopicTestSuite) Test_a_fifo_topic_takes_a_group_and_a_deduplication_id() {
	api := &fakeSNS{}

	_, err := s.topic(api).Publish(awstest.Ctx(), []byte("body"),
		func(o *topic.SendOptions) { o.GroupID = "orders" },
		func(o *topic.SendOptions) { o.DeduplicationID = "order-1" },
		func(o *topic.SendOptions) { o.Attributes = map[string]string{"kind": "created"} },
	)

	s.Require().NoError(err)
	s.Equal("orders", aws.ToString(api.published.MessageGroupId))
	s.Equal("order-1", aws.ToString(api.published.MessageDeduplicationId))
	s.Equal("created", aws.ToString(api.published.MessageAttributes["kind"].StringValue))
}

func (s *TopicTestSuite) Test_a_subject_becomes_what_sns_calls_it() {
	api := &fakeSNS{}

	_, err := s.topic(api).Publish(awstest.Ctx(), []byte("body"),
		func(o *topic.SendOptions) {
			o.Subject = "An order was created"
		})

	s.Require().NoError(err)
	s.Equal("An order was created", aws.ToString(api.published.Subject))
}

func (s *TopicTestSuite) Test_a_publish_without_a_subject_carries_none() {
	api := &fakeSNS{}

	_, err := s.topic(api).Publish(awstest.Ctx(), []byte("body"))

	s.Require().NoError(err)
	s.Nil(api.published.Subject)
}

func (s *TopicTestSuite) Test_a_batch_is_published_in_as_few_requests_as_sns_allows() {
	api := &fakeSNS{}
	entries := make([]topic.BatchEntry, 23)
	for i := range entries {
		entries[i] = topic.BatchEntry{ID: strconv.Itoa(i), Body: fmt.Appendf(nil, "body-%d", i)}
	}

	result, err := s.topic(api).PublishBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Require().Len(api.batches, 3, "10 at a time is SNS's own ceiling")
	s.Len(api.batches[0].PublishBatchRequestEntries, 10)
	s.Len(api.batches[2].PublishBatchRequestEntries, 3)
	s.Len(result.Successful, 23)
	s.Empty(result.Failed)
}

func (s *TopicTestSuite) Test_a_result_carries_the_name_the_caller_gave_each_entry() {
	// SNS answers with successes and failures in neither case ordered, so an
	// answer has to be matched back to what was published rather than appended
	// to.
	api := &fakeSNS{}
	entries := []topic.BatchEntry{
		{ID: "first", Body: []byte("a")},
		{ID: "second", Body: []byte("b")},
		{ID: "third", Body: []byte("c")},
	}

	result, err := s.topic(api).PublishBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Equal([]topic.BatchSuccess{
		{ID: "third", MessageID: "message-2"},
		{ID: "second", MessageID: "message-1"},
		{ID: "first", MessageID: "message-0"},
	}, result.Successful, "in the order SNS answered, named as the caller named them")
}

func (s *TopicTestSuite) Test_entries_are_unique_in_delivery_regardless_of_caller_set_ids() {
	// SNS holds the ids of one request to a narrow alphabet and requires them
	// to be unique, so the entries are addressed by position and the caller's
	// own names never reach it.
	api := &fakeSNS{}
	entries := []topic.BatchEntry{
		{ID: "orders/created @ 1", Body: []byte("a")},
		{ID: "orders/created @ 1", Body: []byte("b")},
	}

	result, err := s.topic(api).PublishBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Equal([]string{"0", "1"}, []string{
		aws.ToString(api.batches[0].PublishBatchRequestEntries[0].Id),
		aws.ToString(api.batches[0].PublishBatchRequestEntries[1].Id),
	})
	s.Len(result.Successful, 2)
}

func (s *TopicTestSuite) Test_a_refused_entry_is_reported_rather_than_raised() {
	// SNS takes a batch partially. A caller handed an error for the whole thing
	// cannot tell which of their messages went, and retrying would publish the
	// ones that did a second time.
	api := &fakeSNS{answer: func(in *sns.PublishBatchInput) *sns.PublishBatchOutput {
		return &sns.PublishBatchOutput{
			Successful: []snstypes.PublishBatchResultEntry{{
				Id: in.PublishBatchRequestEntries[0].Id, MessageId: aws.String("message-0"),
			}},
			Failed: []snstypes.BatchResultErrorEntry{{
				Id:          in.PublishBatchRequestEntries[1].Id,
				Code:        aws.String("InvalidParameter"),
				Message:     aws.String("the message is too large"),
				SenderFault: true,
			}},
		}
	}}
	entries := []topic.BatchEntry{{ID: "a", Body: []byte("a")}, {ID: "b", Body: []byte("b")}}

	result, err := s.topic(api).PublishBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Equal([]topic.BatchSuccess{{ID: "a", MessageID: "message-0"}}, result.Successful)
	s.Equal([]topic.BatchFailure{{
		ID:          "b",
		Code:        "InvalidParameter",
		Message:     "the message is too large",
		SenderFault: true,
	}}, result.Failed)
}

func (s *TopicTestSuite) Test_a_failed_request_accounts_for_every_entry() {
	api := &fakeSNS{failFrom: 2}
	entries := make([]topic.BatchEntry, 15)
	for i := range entries {
		entries[i] = topic.BatchEntry{ID: strconv.Itoa(i), Body: []byte("body")}
	}

	result, err := s.topic(api).PublishBatch(awstest.Ctx(), entries)

	s.Require().Error(err)
	s.Contains(err.Error(), `topic "ordersTopic"`)
	s.Len(result.Successful, 10, "the first request's entries went and are reported")
	s.Require().Len(result.Unsent, 5, "the failed request's entries are reported as unsent")
	s.Equal("10", result.Unsent[0].ID)
	s.ErrorIs(result.Unsent[0].Err, err, "the failure that stopped the batch")
	s.Empty(result.Failed, "SNS refused nothing: it was never asked")
}

func (s *TopicTestSuite) Test_the_options_of_one_entry_apply_to_that_entry_alone() {
	api := &fakeSNS{}
	entries := []topic.BatchEntry{
		{ID: "a", Body: []byte("a"), Options: []topic.SendOption{
			func(o *topic.SendOptions) { o.GroupID = "orders" },
			func(o *topic.SendOptions) { o.Subject = "An order was created" },
		}},
		{ID: "b", Body: []byte("b")},
	}

	_, err := s.topic(api).PublishBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	published := api.batches[0].PublishBatchRequestEntries
	s.Equal("orders", aws.ToString(published[0].MessageGroupId))
	s.Equal("An order was created", aws.ToString(published[0].Subject))
	s.Nil(published[1].MessageGroupId, "a standard topic refuses a request carrying one")
	s.Nil(published[1].Subject)
}

func (s *TopicTestSuite) Test_an_empty_batch_publishes_nothing() {
	api := &fakeSNS{}

	result, err := s.topic(api).PublishBatch(awstest.Ctx(), nil)

	s.Require().NoError(err)
	s.Empty(result.Successful)
	s.Empty(result.Failed)
	s.Empty(api.batches, "an empty batch is not a request worth making")
}
