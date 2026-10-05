package topic

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Stated here rather than left to the builder that returns one, so that an
// operation the contract gained and this package has not is a failure naming
// the type rather than one naming whichever call site first wanted it.
var _ topic.Client = (*snsTopic)(nil)

type snsTopic struct {
	topics *topics
	ref    resources.Ref
}

func (t *snsTopic) Publish(
	ctx context.Context, body []byte, opts ...topic.SendOption,
) (string, error) {
	options := publishOptions(opts)

	arn, client, err := t.resolve(ctx)
	if err != nil {
		return "", err
	}

	in := &sns.PublishInput{
		TopicArn: aws.String(arn),
		Message:  aws.String(string(body)),
		Subject:  subject(options),
	}
	applyFIFO(options, &in.MessageGroupId, &in.MessageDeduplicationId)
	if attrs := publishAttributes(options); attrs != nil {
		in.MessageAttributes = attrs
	}

	out, err := client.Publish(ctx, in)
	if err != nil {
		return "", fmt.Errorf("celerity: publishing to %s: %w", t.ref, err)
	}
	return aws.ToString(out.MessageId), nil
}

// PublishBatch publishes several messages, in as few requests as SNS allows.
//
// Entries SNS refuses are reported rather than raised: it takes a batch
// partially, and a caller handed back an error for the whole thing cannot tell
// which of their messages went. Where a request does not complete at all, the
// entries it carried and any the batch never got to are reported as unsent, so
// every entry is accounted for either way.
func (t *snsTopic) PublishBatch(
	ctx context.Context, entries []topic.BatchEntry,
) (topic.BatchResult, error) {
	var result topic.BatchResult
	if len(entries) == 0 {
		return result, nil
	}

	arn, client, err := t.resolve(ctx)
	if err != nil {
		result.Unsent = unsent(entries, err)
		return result, err
	}

	for start := 0; start < len(entries); start += maxBatchEntries {
		end := min(start+maxBatchEntries, len(entries))
		chunk, err := t.publishChunk(ctx, client, arn, entries[start:end])
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
func unsent(entries []topic.BatchEntry, err error) []topic.BatchUnsent {
	out := make([]topic.BatchUnsent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, topic.BatchUnsent{ID: entry.ID, Err: err})
	}
	return out
}

// maxBatchEntries is SNS's own ceiling on one PublishBatch request.
const maxBatchEntries = 10

func (t *snsTopic) publishChunk(
	ctx context.Context,
	client API,
	arn string,
	entries []topic.BatchEntry,
) (topic.BatchResult, error) {
	var result topic.BatchResult

	out, err := client.PublishBatch(ctx, &sns.PublishBatchInput{
		TopicArn:                   aws.String(arn),
		PublishBatchRequestEntries: batchEntries(entries),
	})
	if err != nil {
		return result, fmt.Errorf("celerity: publishing a batch to %s: %w", t.ref, err)
	}

	for _, entry := range out.Successful {
		index, ok := indexOf(entry.Id, entries)
		if !ok {
			return result, t.unknownEntry(entry.Id)
		}
		result.Successful = append(result.Successful, topic.BatchSuccess{
			ID:        entries[index].ID,
			MessageID: aws.ToString(entry.MessageId),
		})
	}

	for _, entry := range out.Failed {
		index, ok := indexOf(entry.Id, entries)
		if !ok {
			return result, t.unknownEntry(entry.Id)
		}
		result.Failed = append(result.Failed, topic.BatchFailure{
			ID:          entries[index].ID,
			Code:        aws.ToString(entry.Code),
			Message:     aws.ToString(entry.Message),
			SenderFault: entry.SenderFault,
		})
	}
	return result, nil
}

// batchEntries converts one chunk to what SNS takes.
//
// Each entry is addressed by its position in the chunk rather than by the name
// the caller gave it. SNS requires the ids of one request to be unique and
// drawn from a narrow alphabet, neither of which a caller naming their own
// entries can be held to, and an id is only needed to match an answer back to
// what was sent.
func batchEntries(entries []topic.BatchEntry) []snstypes.PublishBatchRequestEntry {
	out := make([]snstypes.PublishBatchRequestEntry, 0, len(entries))
	for i, entry := range entries {
		options := publishOptions(entry.Options)
		converted := snstypes.PublishBatchRequestEntry{
			Id:      aws.String(strconv.Itoa(i)),
			Message: aws.String(string(entry.Body)),
			Subject: subject(options),
		}
		applyFIFO(options, &converted.MessageGroupId, &converted.MessageDeduplicationId)
		if attrs := publishAttributes(options); attrs != nil {
			converted.MessageAttributes = attrs
		}
		out = append(out, converted)
	}
	return out
}

func indexOf(id *string, entries []topic.BatchEntry) (int, bool) {
	index, err := strconv.Atoi(aws.ToString(id))
	if err != nil || index < 0 || index >= len(entries) {
		return 0, false
	}
	return index, true
}

func (t *snsTopic) unknownEntry(id *string) error {
	return fmt.Errorf(
		"celerity: publishing a batch to %s: SNS answered for an entry %q that was not sent",
		t.ref, aws.ToString(id))
}

func (t *snsTopic) resolve(ctx context.Context) (string, API, error) {
	arn, err := t.ref.ID(ctx)
	if err != nil {
		return "", nil, err
	}

	key, err := service.KeyFor(ctx, t.ref)
	if err != nil {
		return "", nil, err
	}

	client, err := t.topics.sns(ctx, key)
	if err != nil {
		return "", nil, err
	}

	return arn, client, nil
}

// applyFIFO sets the two fields only a FIFO topic takes. A standard one refuses
// a request carrying either, so they are set only when asked for.
func applyFIFO(options topic.SendOptions, group, dedup **string) {
	if options.GroupID != "" {
		*group = aws.String(options.GroupID)
	}
	if options.DeduplicationID != "" {
		*dedup = aws.String(options.DeduplicationID)
	}
}

func subject(options topic.SendOptions) *string {
	if options.Subject == "" {
		return nil
	}

	return aws.String(options.Subject)
}

// SNS takes the same attribute shape as SQS under a type of its own, so this
// is the one difference rather than a second conversion.
func publishAttributes(options topic.SendOptions) map[string]snstypes.MessageAttributeValue {
	if len(options.Attributes) == 0 {
		return nil
	}

	attrs := make(map[string]snstypes.MessageAttributeValue, len(options.Attributes))
	for name, value := range options.Attributes {
		attrs[name] = snstypes.MessageAttributeValue{
			DataType:    aws.String("String"),
			StringValue: aws.String(value),
		}
	}
	return attrs
}

// publishOptions resolves a publish's options. Separate from the queue's
// because a topic's options are their own type, so that the two can diverge
// where the services do.
func publishOptions(opts []topic.SendOption) topic.SendOptions {
	var options topic.SendOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}
