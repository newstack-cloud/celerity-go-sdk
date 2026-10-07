package celeritytest

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// PublishedMessage is one message a handler published to a [Topic].
type PublishedMessage struct {
	// ID is what the topic called it, which is what the publish returned.
	ID   string
	Body []byte
	// Options are what the publish asked for, resolved.
	Options topic.SendOptions
}

// Text is the body as a string, for the ordinary case of asserting on one.
func (m PublishedMessage) Text() string {
	return string(m.Body)
}

// Topic is publish-subscribe held in memory.
//
// Nothing subscribes to it: what a test asserts on is what a handler published,
// so the messages are kept rather than delivered.
type Topic struct {
	ref resources.Ref

	mu        sync.Mutex
	published []PublishedMessage
	ids       int

	// Refuse fails the next publish, for a test exercising what a handler does
	// when a topic will not take a message. Cleared once it has fired.
	Refuse error
}

// NewTopic returns an empty topic.
func NewTopic(name string) *Topic {
	return &Topic{ref: resources.Ref{Kind: resources.KindTopic, Name: name}}
}

// Published returns the messages a handler published, in order.
func (t *Topic) Published() []PublishedMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]PublishedMessage(nil), t.published...)
}

// Len reports how many messages were published.
func (t *Topic) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.published)
}

// Reset forgets what was published, for a test reusing one topic across cases.
func (t *Topic) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.published = nil
}

func (t *Topic) Publish(
	ctx context.Context, body []byte, opts ...topic.SendOption,
) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.takeRefusal(); err != nil {
		return "", err
	}
	return t.record(body, opts), nil
}

func (t *Topic) PublishBatch(
	ctx context.Context, entries []topic.BatchEntry,
) (topic.BatchResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	result := topic.BatchResult{}
	if err := t.takeRefusal(); err != nil {
		for _, entry := range entries {
			result.Unsent = append(result.Unsent, topic.BatchUnsent{ID: entry.ID, Err: err})
		}
		return result, err
	}

	for _, entry := range entries {
		id := t.record(entry.Body, entry.Options)
		result.Successful = append(result.Successful, topic.BatchSuccess{
			ID: entry.ID, MessageID: id,
		})
	}
	return result, nil
}

func (t *Topic) record(body []byte, opts []topic.SendOption) string {
	var options topic.SendOptions
	for _, opt := range opts {
		opt(&options)
	}

	t.ids++
	id := fmt.Sprintf("%s-%d", t.ref.Name, t.ids)
	t.published = append(t.published, PublishedMessage{
		ID:      id,
		Body:    bytes.Clone(body),
		Options: options,
	})
	return id
}

func (t *Topic) takeRefusal() error {
	err := t.Refuse
	t.Refuse = nil
	return err
}
