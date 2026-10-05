// The queue package contains the provider-agnostic interface to a point-to-point queue:
// SQS, Cloud Tasks, Service Bus.
//
// A handle is taken by naming the blueprint resource:
//
//	work := resources.Queue(app, "workQueue")
//
// Implementations are per provider and live in their own modules, such as
// resources/aws.
package queue

import (
	"context"
	"time"
)

// Client is a point-to-point queue (e.g. SQS, Cloud Tasks, Service Bus).
type Client interface {
	// Send puts one message on the queue, and returns the provider's id for it.
	Send(ctx context.Context, body []byte, opts ...SendOption) (string, error)
	// SendBatch sends several messages in as few requests as the provider
	// allows. Every entry comes back in exactly one of the three lists of
	// [BatchResult], whether or not an error comes back with it.
	//
	// A batch taken in part is not an error: a provider accepts the entries it
	// will and refuses the rest, and those refusals are Failed.
	//
	// The error reports a request that did not complete. A batch over the
	// provider's own ceiling is more than one request, and it stops at the
	// first that fails: entries sent before then are in Successful, and the
	// entries of the failed request along with any the batch never reached are
	// in Unsent.
	SendBatch(ctx context.Context, entries []BatchEntry) (BatchResult, error)
}

// BatchEntry is one message of a batch send.
type BatchEntry struct {
	// ID is the caller's own name for this entry, which the result carries back.
	//
	// The result is not ordered, so this is the only thing that identifies an
	// entry in it. Not sent to the provider, so any string the caller can tell
	// apart will do and two entries may share one.
	ID string
	// Body is the message.
	Body []byte
	// Options configure this entry alone rather than the batch, so one batch can
	// span message groups.
	Options []SendOption
}

// BatchResult reports what became of each entry of a batch.
//
// Every entry handed over appears in exactly one of the three lists: one the
// provider took, one it was asked about and refused, and one it was never able
// to answer for.
type BatchResult struct {
	Successful []BatchSuccess
	Failed     []BatchFailure
	Unsent     []BatchUnsent
}

// BatchSuccess is an entry the provider took.
type BatchSuccess struct {
	// ID is the name the caller gave the entry.
	ID string
	// MessageID is what the provider called the message it took.
	MessageID string
}

// BatchUnsent is an entry the provider was never able to answer for.
//
// Not a refusal: the provider said nothing about it, because the request
// carrying it did not complete, as an earlier request did not and the rest
// were not attempted, or because the batch was stopped before it began.
type BatchUnsent struct {
	// ID is the name the caller gave the entry.
	ID string
	// Err is what stopped the batch, and is the same failure for every unsent
	// entry.
	//
	// Whether the entry is worth sending again is Err's to say: a request that
	// timed out is, an entry the provider cannot accept is not until it is
	// changed. An entry of the request that actually failed may or may not have
	// reached the provider, which is a reason to send it again rather than a
	// reason not to: a message delivered twice is something a consumer has to
	// withstand regardless.
	Err error
}

// BatchFailure is an entry the provider refused.
type BatchFailure struct {
	// ID is the name the caller gave the entry.
	ID string
	// Code is the provider's own name for the refusal.
	Code string
	// Message describes the refusal.
	Message string
	// SenderFault reports whether the entry itself was at fault rather than the
	// service, where the provider tells the two apart. A sender fault fails the
	// same way however often it is retried; a service fault may not.
	SenderFault bool
}

// SendOption configures a send.
type SendOption func(*SendOptions)

// SendOptions is the resolved configuration for a send.
type SendOptions struct {
	Delay      time.Duration
	Attributes map[string]string
	// GroupID orders messages within a group on queues that support it.
	GroupID string
	// DeduplicationID suppresses a duplicate send within the provider's window.
	DeduplicationID string
}
