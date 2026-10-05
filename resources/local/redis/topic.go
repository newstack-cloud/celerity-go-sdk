package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Stated here rather than left to the provider that returns one, so that an
// operation the contract gained and this package has not is a failure naming
// the type.
var _ topic.Client = (*redisTopic)(nil)

// redisTopic is publish-subscribe as a Redis channel.
//
// A channel rather than a stream, because that is what a topic is: a publish
// reaches whoever is subscribed at the time and is not kept for whoever is not,
// which is what SNS does and what the runtime's local subscriber expects.
type redisTopic struct {
	conn *connection
	ref  resources.Ref
}

// channelKey is how the runtime names the channel for a topic. Byte for byte
// what the subscriber listens on, so a mismatch is a message published into
// nothing.
func channelKey(name string) string {
	return "celerity:topic:channel:" + name
}

func (t *redisTopic) Publish(
	ctx context.Context, body []byte, opts ...topic.SendOption,
) (string, error) {
	client, channel, err := t.resolve(ctx)
	if err != nil {
		return "", err
	}

	var options topic.SendOptions
	for _, opt := range opts {
		opt(&options)
	}
	id, payload, err := envelope(body, options)
	if err != nil {
		return "", fmt.Errorf("celerity: publishing to %s: %w", t.ref, err)
	}

	if err := client.Publish(ctx, channel, payload).Err(); err != nil {
		return "", fmt.Errorf("celerity: publishing to %s: %w", t.ref, err)
	}
	return id, nil
}

func (t *redisTopic) PublishBatch(
	ctx context.Context, entries []topic.BatchEntry,
) (topic.BatchResult, error) {
	var result topic.BatchResult
	if len(entries) == 0 {
		return result, nil
	}

	client, channel, err := t.resolve(ctx)
	if err != nil {
		result.Unsent = unsentPublishes(entries, err)
		return result, err
	}

	pipeline := client.Pipeline()
	published := make([]*goredis.IntCmd, len(entries))
	ids := make([]string, len(entries))
	for i, entry := range entries {
		var options topic.SendOptions
		for _, opt := range entry.Options {
			opt(&options)
		}

		id, payload, err := envelope(entry.Body, options)
		if err != nil {
			wrapped := fmt.Errorf("celerity: publishing a batch to %s: %w", t.ref, err)
			result.Unsent = unsentPublishes(entries, wrapped)
			return result, wrapped
		}
		ids[i] = id
		published[i] = pipeline.Publish(ctx, channel, payload)
	}

	if _, err := pipeline.Exec(ctx); err != nil && !isCommandFailure(err) {
		wrapped := fmt.Errorf("celerity: publishing a batch to %s: %w", t.ref, err)
		result.Unsent = unsentPublishes(entries, wrapped)
		return result, wrapped
	}

	for i, entry := range entries {
		if err := published[i].Err(); err != nil {
			result.Failed = append(result.Failed, topic.BatchFailure{
				ID:      entry.ID,
				Code:    "RedisError",
				Message: err.Error(),
			})
			continue
		}
		result.Successful = append(result.Successful, topic.BatchSuccess{
			ID:        entry.ID,
			MessageID: ids[i],
		})
	}
	return result, nil
}

func (t *redisTopic) resolve(ctx context.Context) (goredis.UniversalClient, string, error) {
	name, err := t.ref.ID(ctx)
	if err != nil {
		return nil, "", err
	}
	client, err := t.conn.get()
	if err != nil {
		return nil, "", err
	}
	return client, channelKey(name), nil
}

// envelope is the JSON the session's bridge parses, and the id it carries.
//
// The bridge subscribes to the channel, reads this, and writes a stream entry
// per consumer, so these field names are the contract: a payload it cannot
// parse is relayed with the whole thing as the body, which would reach a
// handler as JSON it never sent.
//
// A publish has no id of its own on a channel: Redis answers with how many
// subscribers received it rather than with a name for what was sent. So one is
// made here, which is what the contract promises a publish returns and what the
// bridge copies onto the entry.
func envelope(body []byte, options topic.SendOptions) (string, []byte, error) {
	if !utf8.Valid(body) {
		// There is nowhere to say otherwise. The bridge marks every entry it
		// relays as text, so bytes that are not text would reach a handler
		// encoded as something it has no way to know it should decode. A
		// session cannot carry this, and saying so is better than delivering
		// base64 that looks like a message.
		return "", nil, errors.New(
			"a topic in a development session carries text: the session relays a publish " +
				"through a channel that has no way to mark a body as binary, so bytes that " +
				"are not valid UTF-8 cannot be published until the session can say so")
	}

	id, err := messageID()
	if err != nil {
		return "", nil, err
	}

	payload := map[string]any{"messageId": id, "body": string(body)}
	if options.Subject != "" {
		payload["subject"] = options.Subject
	}
	if len(options.Attributes) > 0 {
		payload["attributes"] = options.Attributes
	}

	marshalled, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	return id, marshalled, nil
}

// messageID names one published message.
//
// A version 4 UUID, which is what the Node and Python SDKs put in this field.
func messageID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("naming a message: %w", err)
	}
	return id.String(), nil
}

func unsentPublishes(entries []topic.BatchEntry, err error) []topic.BatchUnsent {
	out := make([]topic.BatchUnsent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, topic.BatchUnsent{ID: entry.ID, Err: err})
	}
	return out
}
