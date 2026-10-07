package celeritytest

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
)

// SentMessage is one message a handler put on a [Queue].
type SentMessage struct {
	// ID is what the queue called it, which is what the send returned.
	ID   string
	Body []byte
	// Options are what the send asked for, resolved.
	Options queue.SendOptions
}

// Text is the body as a string, for the ordinary case of asserting on one.
func (m SentMessage) Text() string {
	return string(m.Body)
}

// Queue is a point-to-point queue held in memory.
//
// Nothing consumes from it: what a test asserts on is what a handler sent, so
// the messages are kept rather than delivered.
type Queue struct {
	ref resources.Ref

	mu   sync.Mutex
	sent []SentMessage
	ids  int

	// Refuse fails the next send, for a test exercising what a handler does
	// when a queue will not take a message. Cleared once it has fired.
	Refuse error
}

// NewQueue returns an empty queue.
func NewQueue(name string) *Queue {
	return &Queue{ref: resources.Ref{Kind: resources.KindQueue, Name: name}}
}

// Sent returns the messages a handler sent, in order.
func (q *Queue) Sent() []SentMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]SentMessage(nil), q.sent...)
}

// Len reports how many messages were sent.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.sent)
}

// Reset forgets what was sent, for a test reusing one queue across cases.
func (q *Queue) Reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sent = nil
}

func (q *Queue) Send(
	ctx context.Context, body []byte, opts ...queue.SendOption,
) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if err := q.takeRefusal(); err != nil {
		return "", err
	}
	return q.record(body, opts), nil
}

func (q *Queue) SendBatch(
	ctx context.Context, entries []queue.BatchEntry,
) (queue.BatchResult, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	result := queue.BatchResult{}
	if err := q.takeRefusal(); err != nil {
		// Every entry is unsent: the request carrying them did not complete,
		// which is the shape a caller has to be able to retry from.
		for _, entry := range entries {
			result.Unsent = append(result.Unsent, queue.BatchUnsent{ID: entry.ID, Err: err})
		}
		return result, err
	}

	for _, entry := range entries {
		id := q.record(entry.Body, entry.Options)
		result.Successful = append(result.Successful, queue.BatchSuccess{
			ID: entry.ID, MessageID: id,
		})
	}
	return result, nil
}

func (q *Queue) record(body []byte, opts []queue.SendOption) string {
	var options queue.SendOptions
	for _, opt := range opts {
		opt(&options)
	}

	q.ids++
	id := fmt.Sprintf("%s-%d", q.ref.Name, q.ids)
	q.sent = append(q.sent, SentMessage{
		ID:      id,
		Body:    bytes.Clone(body),
		Options: options,
	})
	return id
}

// takeRefusal fires a configured failure once, so a test arranging one does not
// have to clear it before the next send.
func (q *Queue) takeRefusal() error {
	err := q.Refuse
	q.Refuse = nil
	return err
}
