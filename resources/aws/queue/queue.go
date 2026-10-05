package queue

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

// Stated here rather than left to the builder that returns one, so that an
// operation the contract gained and this package has not is a failure naming
// the type rather than one naming whichever call site first wanted it.
var _ queue.Client = (*sqsQueue)(nil)

// A point-to-point queue on SQS. The identifier a deployment records
// for one is its URL, which is what every call addresses.
type sqsQueue struct {
	queues *queues
	ref    resources.Ref
}

func (q *sqsQueue) Send(
	ctx context.Context, body []byte, opts ...queue.SendOption,
) (string, error) {
	client, url, err := q.resolve(ctx)
	if err != nil {
		return "", err
	}

	options := sendOptions(opts)
	in := &sqs.SendMessageInput{
		QueueUrl:     aws.String(url),
		MessageBody:  aws.String(string(body)),
		DelaySeconds: delaySeconds(options),
	}
	applyFIFO(options, &in.MessageGroupId, &in.MessageDeduplicationId)
	if attrs := messageAttributes(options); attrs != nil {
		in.MessageAttributes = attrs
	}

	out, err := client.SendMessage(ctx, in)
	if err != nil {
		return "", fmt.Errorf("celerity: sending to %s: %w", q.ref, err)
	}
	return aws.ToString(out.MessageId), nil
}

// SendBatch sends several messages, in as few requests as SQS allows.
//
// Entries SQS refuses are reported rather than raised: it takes a batch
// partially, and a caller handed back an error for the whole thing cannot tell
// which of their messages went. Where a request does not complete at all, the
// entries it carried and any the batch never got to are reported as unsent, so
// every entry is accounted for either way.
func (q *sqsQueue) SendBatch(
	ctx context.Context, entries []queue.BatchEntry,
) (queue.BatchResult, error) {
	var result queue.BatchResult
	if len(entries) == 0 {
		return result, nil
	}

	client, url, err := q.resolve(ctx)
	if err != nil {
		// Nothing was attempted, so nothing is in doubt: the whole batch is
		// unsent rather than the resolution failure being the only answer.
		result.Unsent = unsent(entries, err)
		return result, err
	}

	for start := 0; start < len(entries); start += maxBatchEntries {
		end := min(start+maxBatchEntries, len(entries))
		chunk, err := q.sendChunk(ctx, client, url, entries[start:end])
		if err != nil {
			result.Unsent = unsent(entries[start:], err)
			return result, err
		}
		result.Successful = append(result.Successful, chunk.Successful...)
		result.Failed = append(result.Failed, chunk.Failed...)
	}
	return result, nil
}

// unsent accounts for the entries from the request that failed onwards, which
// is the failed request's own and every one the batch never got to.
func unsent(entries []queue.BatchEntry, err error) []queue.BatchUnsent {
	out := make([]queue.BatchUnsent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, queue.BatchUnsent{ID: entry.ID, Err: err})
	}
	return out
}

// maxBatchEntries is SQS's own ceiling on one SendMessageBatch request.
const maxBatchEntries = 10

func (q *sqsQueue) sendChunk(
	ctx context.Context,
	client API,
	url string,
	entries []queue.BatchEntry,
) (queue.BatchResult, error) {
	var result queue.BatchResult

	in := &sqs.SendMessageBatchInput{
		QueueUrl: aws.String(url),
		Entries:  batchEntries(entries),
	}
	out, err := client.SendMessageBatch(ctx, in)
	if err != nil {
		return result, fmt.Errorf("celerity: sending a batch to %s: %w", q.ref, err)
	}

	for _, entry := range out.Successful {
		index, ok := indexOf(entry.Id, entries)
		if !ok {
			return result, q.unknownEntry(entry.Id)
		}
		result.Successful = append(result.Successful, queue.BatchSuccess{
			ID:        entries[index].ID,
			MessageID: aws.ToString(entry.MessageId),
		})
	}

	for _, entry := range out.Failed {
		index, ok := indexOf(entry.Id, entries)
		if !ok {
			return result, q.unknownEntry(entry.Id)
		}
		result.Failed = append(result.Failed, queue.BatchFailure{
			ID:          entries[index].ID,
			Code:        aws.ToString(entry.Code),
			Message:     aws.ToString(entry.Message),
			SenderFault: entry.SenderFault,
		})
	}

	return result, nil
}

// batchEntries converts one chunk to what SQS takes.
//
// Each entry is addressed by its position in the chunk rather than by the name
// the caller gave it. SQS requires the ids of one request to be unique and
// drawn from a narrow alphabet, neither of which a caller naming their own
// entries can be held to, and an id is only needed to match an answer back to
// what was sent.
func batchEntries(entries []queue.BatchEntry) []sqstypes.SendMessageBatchRequestEntry {
	out := make([]sqstypes.SendMessageBatchRequestEntry, 0, len(entries))
	for i, entry := range entries {
		options := sendOptions(entry.Options)
		converted := sqstypes.SendMessageBatchRequestEntry{
			Id:           aws.String(strconv.Itoa(i)),
			MessageBody:  aws.String(string(entry.Body)),
			DelaySeconds: delaySeconds(options),
		}
		applyFIFO(options, &converted.MessageGroupId, &converted.MessageDeduplicationId)
		if attrs := messageAttributes(options); attrs != nil {
			converted.MessageAttributes = attrs
		}
		out = append(out, converted)
	}
	return out
}

func indexOf(id *string, entries []queue.BatchEntry) (int, bool) {
	index, err := strconv.Atoi(aws.ToString(id))
	if err != nil || index < 0 || index >= len(entries) {
		return 0, false
	}
	return index, true
}

func (q *sqsQueue) unknownEntry(id *string) error {
	return fmt.Errorf(
		"celerity: sending a batch to %s: SQS answered for an entry %q that was not sent",
		q.ref, aws.ToString(id))
}

func (q *sqsQueue) resolve(ctx context.Context) (API, string, error) {
	url, err := q.ref.ID(ctx)
	if err != nil {
		return nil, "", err
	}

	key, err := service.KeyFor(ctx, q.ref)
	if err != nil {
		return nil, "", err
	}

	client, err := q.queues.sqs(ctx, key)
	if err != nil {
		return nil, "", err
	}

	return client, url, nil
}

func sendOptions(opts []queue.SendOption) queue.SendOptions {
	var options queue.SendOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// delaySeconds converts a delay to what SQS takes, which is whole seconds.
//
// Truncated rather than rounded: a delay is the earliest a message should
// become visible, and rounding up would hold back one nobody asked to hold.
func delaySeconds(options queue.SendOptions) int32 {
	if options.Delay <= 0 {
		return 0
	}
	return int32(options.Delay.Seconds())
}

// applyFIFO sets the two fields only a FIFO queue or topic takes. A standard
// one refuses a request carrying either, so they are set only when asked for.
func applyFIFO(options queue.SendOptions, group, dedup **string) {
	if options.GroupID != "" {
		*group = aws.String(options.GroupID)
	}
	if options.DeduplicationID != "" {
		*dedup = aws.String(options.DeduplicationID)
	}
}

func messageAttributes(options queue.SendOptions) map[string]sqstypes.MessageAttributeValue {
	if len(options.Attributes) == 0 {
		return nil
	}
	attrs := make(map[string]sqstypes.MessageAttributeValue, len(options.Attributes))
	for name, value := range options.Attributes {
		attrs[name] = sqstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(value),
		}
	}
	return attrs
}
