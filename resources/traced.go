package resources

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// Tracing wraps what a provider built rather than being each provider's to do.
// The underlying backends are also instrumented independently when tracing
// is enabled, this layer correlates traces to the Celerity resource model
// that points to resources defined in a blueprint and used as the primary
// layer of abstraction within applications.

// Adds tracing attributes for Celerity resources.
func resourceAttrs(ref Ref, kind string, extra ...telemetry.Attr) []telemetry.Attr {
	attrs := make([]telemetry.Attr, 0, len(extra)+1)
	attrs = append(attrs, telemetry.String(kind+".resource", ref.Name))
	return append(attrs, extra...)
}

type tracedQueue struct {
	inner queue.Client
	ref   Ref
}

func (q tracedQueue) Send(
	ctx context.Context, body []byte, opts ...queue.SendOption,
) (string, error) {
	return telemetry.Traced(ctx, "celerity.queue.send_message",
		resourceAttrs(q.ref, "queue", telemetry.Int("queue.body_size", len(body))),
		func(ctx context.Context, _ telemetry.Span) (string, error) {
			return q.inner.Send(ctx, body, opts...)
		})
}

func (q tracedQueue) SendBatch(
	ctx context.Context, entries []queue.BatchEntry,
) (queue.BatchResult, error) {
	return telemetry.Traced(ctx, "celerity.queue.send_message_batch",
		resourceAttrs(q.ref, "queue", telemetry.Int("queue.message_count", len(entries))),
		func(ctx context.Context, span telemetry.Span) (queue.BatchResult, error) {
			result, err := q.inner.SendBatch(ctx, entries)
			// A batch taken in part is not an error, so the counts are what say
			// what became of it. Added after the call, since none of them are
			// known before it.
			span.SetAttributes(
				telemetry.Int("queue.successful_count", len(result.Successful)),
				telemetry.Int("queue.failed_count", len(result.Failed)),
				telemetry.Int("queue.unsent_count", len(result.Unsent)),
			)
			return result, err
		})
}

type tracedTopic struct {
	inner topic.Client
	ref   Ref
}

func (t tracedTopic) Publish(
	ctx context.Context, body []byte, opts ...topic.SendOption,
) (string, error) {
	return telemetry.Traced(ctx, "celerity.topic.publish",
		resourceAttrs(t.ref, "topic", telemetry.Int("topic.body_size", len(body))),
		func(ctx context.Context, _ telemetry.Span) (string, error) {
			return t.inner.Publish(ctx, body, opts...)
		})
}

func (t tracedTopic) PublishBatch(
	ctx context.Context, entries []topic.BatchEntry,
) (topic.BatchResult, error) {
	return telemetry.Traced(ctx, "celerity.topic.publish_batch",
		resourceAttrs(t.ref, "topic", telemetry.Int("topic.message_count", len(entries))),
		func(ctx context.Context, span telemetry.Span) (topic.BatchResult, error) {
			result, err := t.inner.PublishBatch(ctx, entries)
			span.SetAttributes(
				telemetry.Int("topic.successful_count", len(result.Successful)),
				telemetry.Int("topic.failed_count", len(result.Failed)),
				telemetry.Int("topic.unsent_count", len(result.Unsent)),
			)
			return result, err
		})
}

type tracedDatabase struct {
	inner sqldb.Client
	ref   Ref
}

// Writer is traced because taking a connection is where the pool is reached
// and where a cold start pays for building one. What is done with the
// connection is not, lower level instrumentation for the SQL driver will
// produce traces for queries made with the connection.
func (d tracedDatabase) Writer(ctx context.Context) (sqldb.Conn, error) {
	return telemetry.Traced(ctx, "celerity.sql_database.writer",
		resourceAttrs(d.ref, "db"),
		func(ctx context.Context, _ telemetry.Span) (sqldb.Conn, error) {
			return d.inner.Writer(ctx)
		})
}

func (d tracedDatabase) Reader(ctx context.Context) (sqldb.Conn, error) {
	return telemetry.Traced(ctx, "celerity.sql_database.reader",
		resourceAttrs(d.ref, "db"),
		func(ctx context.Context, _ telemetry.Span) (sqldb.Conn, error) {
			return d.inner.Reader(ctx)
		})
}

// Stated so that a contract gaining an operation is a failure naming the
// wrapper rather than one naming whichever handle first lacked it.
var (
	_ bucket.Store     = tracedBucket{}
	_ queue.Client     = tracedQueue{}
	_ topic.Client     = tracedTopic{}
	_ datastore.Client = tracedDatastore{}
	_ sqldb.Client     = tracedDatabase{}
	_ cache.Client     = tracedCache{}
)
