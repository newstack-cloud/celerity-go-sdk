package queue_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsqueue "github.com/newstack-cloud/celerity-go-sdk/resources/aws/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

type QueueTestSuite struct {
	suite.Suite
}

func TestQueueTestSuite(t *testing.T) {
	suite.Run(t, new(QueueTestSuite))
}

const ordersQueueURL = "https://sqs.eu-west-2.amazonaws.com/1/orders"

type fakeSQS struct {
	sent    *sqs.SendMessageInput
	batches []*sqs.SendMessageBatchInput

	// answer decides what a batch comes back as, so that the entry ordering
	// SQS does not promise can be reproduced.
	answer func(*sqs.SendMessageBatchInput) *sqs.SendMessageBatchOutput
	err    error

	// failFrom is the batch request, counted from one, that fails and every one
	// after it, so that a batch sent over several requests can fail part way.
	failFrom int
}

func (f *fakeSQS) SendMessage(
	_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options),
) (*sqs.SendMessageOutput, error) {
	f.sent = in
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.SendMessageOutput{MessageId: aws.String("message-1")}, nil
}

func (f *fakeSQS) SendMessageBatch(
	_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options),
) (*sqs.SendMessageBatchOutput, error) {
	f.batches = append(f.batches, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.failFrom > 0 && len(f.batches) >= f.failFrom {
		return nil, errors.New("RequestThrottled")
	}
	if f.answer != nil {
		return f.answer(in), nil
	}
	return acceptAll(in), nil
}

// acceptAll answers the way SQS does when every entry was taken, in an order
// that is not the order they were sent in.
func acceptAll(in *sqs.SendMessageBatchInput) *sqs.SendMessageBatchOutput {
	out := &sqs.SendMessageBatchOutput{}
	for i := len(in.Entries) - 1; i >= 0; i-- {
		out.Successful = append(out.Successful, sqstypes.SendMessageBatchResultEntry{
			Id:        in.Entries[i].Id,
			MessageId: aws.String("message-" + aws.ToString(in.Entries[i].Id)),
		})
	}
	return out
}

func (s *QueueTestSuite) queue(api *fakeSQS) queue.Client {
	q, err := awsqueue.Queues(api)(awstest.SimpleRef(resources.KindQueue, "ordersQueue", ordersQueueURL))
	s.Require().NoError(err)
	return q
}

func (s *QueueTestSuite) Test_the_options_a_send_takes_become_what_sqs_calls_them() {
	api := &fakeSQS{}

	_, err := s.queue(api).Send(awstest.Ctx(), []byte("body"),
		func(o *queue.SendOptions) { o.Delay = 90 * time.Second },
		func(o *queue.SendOptions) { o.GroupID = "orders" },
		func(o *queue.SendOptions) { o.DeduplicationID = "order-1" },
		func(o *queue.SendOptions) { o.Attributes = map[string]string{"kind": "created"} },
	)

	s.Require().NoError(err)
	s.Equal(int32(90), api.sent.DelaySeconds)
	s.Equal("orders", aws.ToString(api.sent.MessageGroupId))
	s.Equal("order-1", aws.ToString(api.sent.MessageDeduplicationId))
	s.Equal("created", aws.ToString(api.sent.MessageAttributes["kind"].StringValue))
	s.Equal("String", aws.ToString(api.sent.MessageAttributes["kind"].DataType))
}

func (s *QueueTestSuite) Test_a_batch_is_sent_in_as_few_requests_as_sqs_allows() {
	api := &fakeSQS{}
	entries := make([]queue.BatchEntry, 23)
	for i := range entries {
		entries[i] = queue.BatchEntry{ID: strconv.Itoa(i), Body: fmt.Appendf(nil, "body-%d", i)}
	}

	result, err := s.queue(api).SendBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Require().Len(api.batches, 3, "10 at a time is SQS's own ceiling")
	s.Len(api.batches[0].Entries, 10)
	s.Len(api.batches[2].Entries, 3)
	s.Len(result.Successful, 23)
	s.Empty(result.Failed)
}

func (s *QueueTestSuite) Test_a_result_carries_the_name_the_caller_gave_each_entry() {
	// SQS answers with successes and failures in neither case ordered, so an
	// answer has to be matched back to what was sent rather than appended to.
	api := &fakeSQS{}
	entries := []queue.BatchEntry{
		{ID: "first", Body: []byte("a")},
		{ID: "second", Body: []byte("b")},
		{ID: "third", Body: []byte("c")},
	}

	result, err := s.queue(api).SendBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Equal([]queue.BatchSuccess{
		{ID: "third", MessageID: "message-2"},
		{ID: "second", MessageID: "message-1"},
		{ID: "first", MessageID: "message-0"},
	}, result.Successful, "in the order SQS answered, named as the caller named them")
}

func (s *QueueTestSuite) Test_an_entry_names_itself_however_the_caller_likes() {
	// SQS holds the ids of one request to a narrow alphabet and requires them
	// to be unique, so the entries are addressed by position and the caller's
	// own names never reach it.
	api := &fakeSQS{}
	entries := []queue.BatchEntry{
		{ID: "orders/created @ 1", Body: []byte("a")},
		{ID: "orders/created @ 1", Body: []byte("b")},
	}

	result, err := s.queue(api).SendBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Equal([]string{"0", "1"}, []string{
		aws.ToString(api.batches[0].Entries[0].Id),
		aws.ToString(api.batches[0].Entries[1].Id),
	})
	s.Len(result.Successful, 2)
}

func (s *QueueTestSuite) Test_a_refused_entry_is_reported_rather_than_raised() {
	// SQS takes a batch partially. A caller handed an error for the whole thing
	// cannot tell which of their messages went, and retrying would send the
	// ones that did a second time.
	api := &fakeSQS{answer: func(in *sqs.SendMessageBatchInput) *sqs.SendMessageBatchOutput {
		return &sqs.SendMessageBatchOutput{
			Successful: []sqstypes.SendMessageBatchResultEntry{{
				Id: in.Entries[0].Id, MessageId: aws.String("message-0"),
			}},
			Failed: []sqstypes.BatchResultErrorEntry{{
				Id:          in.Entries[1].Id,
				Code:        aws.String("InvalidParameterValue"),
				Message:     aws.String("the message is too large"),
				SenderFault: true,
			}},
		}
	}}
	entries := []queue.BatchEntry{{ID: "a", Body: []byte("a")}, {ID: "b", Body: []byte("b")}}

	result, err := s.queue(api).SendBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	s.Equal([]queue.BatchSuccess{{ID: "a", MessageID: "message-0"}}, result.Successful)
	s.Equal([]queue.BatchFailure{{
		ID:          "b",
		Code:        "InvalidParameterValue",
		Message:     "the message is too large",
		SenderFault: true,
	}}, result.Failed)
}

func (s *QueueTestSuite) Test_a_failed_request_accounts_for_every_entry() {
	// A batch over the ceiling is more than one request. A caller told only
	// that the batch failed would send the chunks that went a second time.
	api := &fakeSQS{failFrom: 2}
	entries := make([]queue.BatchEntry, 15)
	for i := range entries {
		entries[i] = queue.BatchEntry{ID: strconv.Itoa(i), Body: []byte("body")}
	}

	result, err := s.queue(api).SendBatch(awstest.Ctx(), entries)

	s.Require().Error(err)
	s.Contains(err.Error(), `queue "ordersQueue"`)
	s.Len(result.Successful, 10, "the first request's entries went and are reported")
	s.Require().Len(result.Unsent, 5, "the failed request's entries are reported as unsent")
	s.Equal("10", result.Unsent[0].ID)
	s.ErrorIs(result.Unsent[0].Err, err, "the failure that stopped the batch")
	s.Empty(result.Failed, "SQS refused nothing: it was never asked")
}

func (s *QueueTestSuite) Test_every_entry_is_accounted_for_exactly_once() {
	// What the three lists are for: a caller reads what to retry rather than
	// working it out by comparing the result against what it handed over.
	api := &fakeSQS{
		failFrom: 2,
		answer: func(in *sqs.SendMessageBatchInput) *sqs.SendMessageBatchOutput {
			out := &sqs.SendMessageBatchOutput{}
			for i, entry := range in.Entries {
				if i == 0 {
					out.Failed = append(out.Failed, sqstypes.BatchResultErrorEntry{
						Id: entry.Id, Code: aws.String("InvalidParameterValue"),
					})
					continue
				}
				out.Successful = append(out.Successful, sqstypes.SendMessageBatchResultEntry{
					Id: entry.Id, MessageId: aws.String("message"),
				})
			}
			return out
		},
	}
	entries := make([]queue.BatchEntry, 15)
	for i := range entries {
		entries[i] = queue.BatchEntry{ID: strconv.Itoa(i), Body: []byte("body")}
	}

	result, _ := s.queue(api).SendBatch(awstest.Ctx(), entries)

	var accounted []string
	for _, sent := range result.Successful {
		accounted = append(accounted, sent.ID)
	}
	for _, refused := range result.Failed {
		accounted = append(accounted, refused.ID)
	}
	for _, missed := range result.Unsent {
		accounted = append(accounted, missed.ID)
	}
	s.ElementsMatch([]string{
		"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12", "13", "14",
	}, accounted)
}

func (s *QueueTestSuite) Test_a_queue_the_deployment_never_recorded_leaves_the_batch_unsent() {
	// Nothing was attempted, so nothing is in doubt and the whole batch is
	// reported rather than the failure being the only answer.
	store, err := awsqueue.Queues(&fakeSQS{})(awstest.Ref(resources.KindQueue, "ordersQueue", nil))
	s.Require().NoError(err)

	result, err := store.SendBatch(awstest.Ctx(), []queue.BatchEntry{
		{ID: "a", Body: []byte("a")},
		{ID: "b", Body: []byte("b")},
	})

	s.Require().Error(err)
	s.Empty(result.Successful)
	s.Require().Len(result.Unsent, 2)
	s.Equal([]string{"a", "b"}, []string{result.Unsent[0].ID, result.Unsent[1].ID})
}

func (s *QueueTestSuite) Test_the_options_of_one_entry_apply_to_that_entry_alone() {
	api := &fakeSQS{}
	entries := []queue.BatchEntry{
		{ID: "a", Body: []byte("a"), Options: []queue.SendOption{
			func(o *queue.SendOptions) { o.GroupID = "orders" },
			func(o *queue.SendOptions) { o.Delay = 30 * time.Second },
		}},
		{ID: "b", Body: []byte("b")},
	}

	_, err := s.queue(api).SendBatch(awstest.Ctx(), entries)

	s.Require().NoError(err)
	sent := api.batches[0].Entries
	s.Equal("orders", aws.ToString(sent[0].MessageGroupId))
	s.Equal(int32(30), sent[0].DelaySeconds)
	s.Nil(sent[1].MessageGroupId, "a standard queue refuses a request carrying one")
	s.Equal(int32(0), sent[1].DelaySeconds)
}

func (s *QueueTestSuite) Test_an_empty_batch_sends_nothing() {
	api := &fakeSQS{}

	result, err := s.queue(api).SendBatch(awstest.Ctx(), nil)

	s.Require().NoError(err)
	s.Empty(result.Successful)
	s.Empty(result.Failed)
	s.Empty(api.batches, "an empty batch is not a request worth making")
}

func (s *QueueTestSuite) Test_a_failure_names_the_resource_the_handler_asked_for() {
	api := &fakeSQS{err: errors.New("AWS.SimpleQueueService.NonExistentQueue")}

	_, err := s.queue(api).Send(awstest.Ctx(), []byte("body"))

	s.Require().Error(err)
	s.Contains(err.Error(), `queue "ordersQueue"`)
	s.Contains(err.Error(), "NonExistentQueue")
}
