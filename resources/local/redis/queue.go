package redis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

// Stated here rather than left to the provider that returns one, so that an
// operation the contract gained and this package has not is a failure naming
// the type.
var _ queue.Client = (*redisQueue)(nil)

// redisQueue is a point-to-point queue as a Redis stream.
//
// A stream rather than a list, because that is what the runtime's consumer
// reads: it claims entries into a consumer group, which is what gives a local
// queue the redelivery and acknowledgement a real one has.
type redisQueue struct {
	conn *connection
	ref  resources.Ref
}

// streamKey is how the runtime names the stream for a queue. Byte for byte what
// the consumer subscribes to, the same way the handler tags are: a mismatch
// means a message that is sent and never arrives.
func streamKey(name string) string {
	return "celerity:queue:" + name
}

func (q *redisQueue) Send(
	ctx context.Context, body []byte, opts ...queue.SendOption,
) (string, error) {
	client, key, err := q.resolve(ctx)
	if err != nil {
		return "", err
	}

	var options queue.SendOptions
	for _, opt := range opts {
		opt(&options)
	}

	id, err := client.XAdd(ctx, &goredis.XAddArgs{
		Stream: key,
		Values: streamFields(body, options),
	}).Result()
	if err != nil {
		return "", fmt.Errorf("celerity: sending to %s: %w", q.ref, err)
	}
	return id, nil
}

func (q *redisQueue) SendBatch(
	ctx context.Context, entries []queue.BatchEntry,
) (queue.BatchResult, error) {
	var result queue.BatchResult
	if len(entries) == 0 {
		return result, nil
	}

	client, key, err := q.resolve(ctx)
	if err != nil {
		result.Unsent = unsentEntries(entries, err)
		return result, err
	}

	// One round trip for the whole batch. Redis has no ceiling on a pipeline the
	// way SQS has one on a request, so there is no chunking here and no partial
	// progress to report: either the pipeline was applied or it was not.
	pipeline := client.Pipeline()
	added := make([]*goredis.StringCmd, len(entries))
	for i, entry := range entries {
		var options queue.SendOptions
		for _, opt := range entry.Options {
			opt(&options)
		}
		added[i] = pipeline.XAdd(ctx, &goredis.XAddArgs{
			Stream: key,
			Values: streamFields(entry.Body, options),
		})
	}

	if _, err := pipeline.Exec(ctx); err != nil {
		// Exec reports the first command that failed, and the commands
		// themselves carry their own answers, so a per-entry error is read
		// below rather than taken from here. A failure to send the pipeline at
		// all leaves every entry unsent.
		if !isCommandFailure(err) {
			wrapped := fmt.Errorf("celerity: sending a batch to %s: %w", q.ref, err)
			result.Unsent = unsentEntries(entries, wrapped)
			return result, wrapped
		}
	}

	for i, entry := range entries {
		id, err := added[i].Result()
		if err != nil {
			result.Failed = append(result.Failed, queue.BatchFailure{
				ID:      entry.ID,
				Code:    "RedisError",
				Message: err.Error(),
				// Redis does not tell a bad command from a failing server, so
				// nothing here can honestly claim the sender was at fault.
			})
			continue
		}
		result.Successful = append(result.Successful, queue.BatchSuccess{
			ID:        entry.ID,
			MessageID: id,
		})
	}
	return result, nil
}

func (q *redisQueue) resolve(ctx context.Context) (goredis.UniversalClient, string, error) {
	name, err := q.ref.ID(ctx)
	if err != nil {
		return nil, "", err
	}
	client, err := q.conn.get()
	if err != nil {
		return nil, "", err
	}
	return client, streamKey(name), nil
}

// streamFields is the entry the runtime's consumer parses.
//
// body and timestamp are required: the consumer reads the stream entry into a
// message and gives up without them. The rest are what it reads when they are
// there. group_id and dedup_id are written although the consumer ignores them,
// so that what a local stream holds says as much about the send as the other
// SDKs' streams do.
//
// A delay is not written, and a local queue does not honour one: a stream entry
// is readable as soon as it is added. Accepted rather than refused because a
// delay is a real queue's and the session is standing in for it, so refusing it
// would fail in a session the code a deployment runs.
func streamFields(body []byte, options queue.SendOptions) map[string]any {
	fields := map[string]any{
		"timestamp": strconv.FormatInt(time.Now().Unix(), 10),
	}

	// The contract carries a type alongside the body because a queue takes
	// bytes and a stream field is a string. Text is the common case and keeps a
	// stream readable while a session is being debugged; anything that is not
	// valid UTF-8 could not survive as a string and is base64 instead.
	if utf8.Valid(body) {
		fields["message_type"] = strconv.Itoa(messageTypeText)
		fields["body"] = string(body)
	} else {
		fields["message_type"] = strconv.Itoa(messageTypeBinary)
		fields["body"] = base64.StdEncoding.EncodeToString(body)
	}

	if options.GroupID != "" {
		fields["group_id"] = options.GroupID
	}
	if options.DeduplicationID != "" {
		fields["dedup_id"] = options.DeduplicationID
	}
	if len(options.Attributes) > 0 {
		// Encoded rather than spread across fields, because the consumer reads
		// one field and decodes it, and a key named by the caller could
		// otherwise collide with one of the fields above.
		if encoded, err := json.Marshal(options.Attributes); err == nil {
			fields["attributes"] = string(encoded)
		}
	}
	return fields
}

// How the runtime says a body was encoded, matching its own RedisMessageType:
// text is a UTF-8 string and binary is base64.
const (
	messageTypeText   = 0
	messageTypeBinary = 1
)

// isCommandFailure reports an Exec error that is one command's own rather than a
// failure to run the pipeline, which is the difference between the entries
// having answers to read and none of them having any.
func isCommandFailure(err error) bool {
	var redisErr goredis.Error
	return errors.As(err, &redisErr)
}

func unsentEntries(entries []queue.BatchEntry, err error) []queue.BatchUnsent {
	out := make([]queue.BatchUnsent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, queue.BatchUnsent{ID: entry.ID, Err: err})
	}
	return out
}
