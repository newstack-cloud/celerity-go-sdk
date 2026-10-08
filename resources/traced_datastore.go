package resources

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

type tracedDatastore struct {
	inner datastore.Client
	ref   Ref
}

// Records which partition an operation reached, and whether it named a
// sort key.
//
// The partition is recorded and the sort key is not, because a partition is a
// tenant or a customer and is what a trace is usually grouped by, while a sort
// key is commonly the identifier of one item and recording it would put a high
// cardinality value on every span.
func keyAttrs(ref Ref, key datastore.Key) []telemetry.Attr {
	return resourceAttrs(ref, "datastore",
		telemetry.String("datastore.partition", key.Partition),
		telemetry.Bool("datastore.sorted", key.Sort != ""),
	)
}

func (d tracedDatastore) Get(
	ctx context.Context, key datastore.Key, out any,
) (datastore.Revision, error) {
	return telemetry.Traced(ctx, "celerity.datastore.get_item", keyAttrs(d.ref, key),
		func(ctx context.Context, _ telemetry.Span) (datastore.Revision, error) {
			return d.inner.Get(ctx, key, out)
		})
}

func (d tracedDatastore) Put(
	ctx context.Context, key datastore.Key, item any, opts ...datastore.WriteOption,
) (datastore.Revision, error) {
	return telemetry.Traced(ctx, "celerity.datastore.put_item",
		conditionAttrs(d.ref, key, opts),
		func(ctx context.Context, _ telemetry.Span) (datastore.Revision, error) {
			return d.inner.Put(ctx, key, item, opts...)
		})
}

func (d tracedDatastore) Delete(
	ctx context.Context, key datastore.Key, opts ...datastore.WriteOption,
) error {
	return telemetry.TracedCall(ctx, "celerity.datastore.delete_item",
		conditionAttrs(d.ref, key, opts),
		func(ctx context.Context, _ telemetry.Span) error {
			return d.inner.Delete(ctx, key, opts...)
		})
}

// conditionAttrs records whether a write was conditional, which is what
// separates a refusal worth retrying from a failure worth reporting.
func conditionAttrs(
	ref Ref, key datastore.Key, opts []datastore.WriteOption,
) []telemetry.Attr {
	options := datastore.ResolveWriteOptions(opts)
	return append(keyAttrs(ref, key),
		telemetry.Bool("datastore.conditional",
			options.Condition != nil || options.Revision != nil))
}

func (d tracedDatastore) Query(
	ctx context.Context, q datastore.Query, out any,
) (datastore.Cursor, error) {
	return telemetry.Traced(ctx, "celerity.datastore.query_page",
		resourceAttrs(d.ref, "datastore",
			telemetry.String("datastore.partition", q.Partition),
			telemetry.String("datastore.index", q.Index),
			// What a query reads less because of, which is the difference
			// between a cost that grows with the partition and one that grows
			// with the answer.
			telemetry.Bool("datastore.sort_condition", q.Sort != nil),
			telemetry.Bool("datastore.filtered", q.Filter != nil),
		),
		func(ctx context.Context, span telemetry.Span) (datastore.Cursor, error) {
			cursor, err := d.inner.Query(ctx, q, out)
			span.SetAttributes(telemetry.Bool("datastore.has_more", cursor.More()))
			return cursor, err
		})
}

// Scan reads every item in the store, so the span is worth having even when
// nothing else is traced: a scan where a query was meant is the expensive
// mistake this package makes possible.
func (d tracedDatastore) Scan(
	ctx context.Context, s datastore.Scan, out any,
) (datastore.Cursor, error) {
	return telemetry.Traced(ctx, "celerity.datastore.scan_page",
		resourceAttrs(d.ref, "datastore",
			telemetry.Bool("datastore.filtered", s.Filter != nil)),
		func(ctx context.Context, span telemetry.Span) (datastore.Cursor, error) {
			cursor, err := d.inner.Scan(ctx, s, out)
			span.SetAttributes(telemetry.Bool("datastore.has_more", cursor.More()))
			return cursor, err
		})
}

func (d tracedDatastore) BatchGet(
	ctx context.Context, keys []datastore.Key, out any,
) ([]datastore.Key, error) {
	return telemetry.Traced(ctx, "celerity.datastore.batch_get_items",
		resourceAttrs(d.ref, "datastore", telemetry.Int("datastore.batch_size", len(keys))),
		func(ctx context.Context, span telemetry.Span) ([]datastore.Key, error) {
			missing, err := d.inner.BatchGet(ctx, keys, out)
			span.SetAttributes(telemetry.Int("datastore.missing_count", len(missing)))
			return missing, err
		})
}

func (d tracedDatastore) Update(
	ctx context.Context, key datastore.Key, updates []datastore.Update,
	opts ...datastore.WriteOption,
) (datastore.Revision, error) {
	return telemetry.Traced(ctx, "celerity.datastore.update_item",
		append(conditionAttrs(d.ref, key, opts),
			telemetry.Int("datastore.update_count", len(updates))),
		func(ctx context.Context, _ telemetry.Span) (datastore.Revision, error) {
			return d.inner.Update(ctx, key, updates, opts...)
		})
}

func (d tracedDatastore) BatchWrite(
	ctx context.Context, ops []datastore.BatchOp,
) ([]datastore.BatchOp, error) {
	return telemetry.Traced(ctx, "celerity.datastore.batch_write_items",
		resourceAttrs(d.ref, "datastore", telemetry.Int("datastore.batch_size", len(ops))),
		func(ctx context.Context, span telemetry.Span) ([]datastore.BatchOp, error) {
			unapplied, err := d.inner.BatchWrite(ctx, ops)
			// A batch write is not atomic on any store, so what could not be
			// applied is what a caller has to act on.
			span.SetAttributes(telemetry.Int("datastore.unapplied_count", len(unapplied)))
			return unapplied, err
		})
}

func (d tracedDatastore) Atomically(
	ctx context.Context, partition string, ops []datastore.AtomicOp,
) error {
	return telemetry.TracedCall(ctx, "celerity.datastore.atomic_write",
		resourceAttrs(d.ref, "datastore",
			telemetry.String("datastore.partition", partition),
			telemetry.Int("datastore.operation_count", len(ops)),
		),
		func(ctx context.Context, _ telemetry.Span) error {
			return d.inner.Atomically(ctx, partition, ops)
		})
}
