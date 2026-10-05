package datastore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// The whole of the interface is implemented here, across this package's
// files. This declaration will catch any changes that break the datastore
// API at compile time.
var _ datastore.Client = (*dynamoStore)(nil)

type dynamoStore struct {
	stores *stores
	ref    resources.Ref

	// The table's key schema, read once. A handler addresses an item by
	// partition and sort value, which is what every document store has, and
	// what those attributes are called is DynamoDB's own and not something the
	// handler should have to repeat at every call site.
	schemaOnce sync.Once
	schema     *tableSchema
	schemaErr  error
}

// Get fills out with the item under the key, and returns the revision to hand
// back to a later write that must not overwrite another.
//
// The revision attribute is taken off the item before it is unmarshalled, so it
// never reaches application code, which is what makes it reserved rather than
// merely undocumented.
func (d *dynamoStore) Get(
	ctx context.Context, key datastore.Key, out any,
) (datastore.Revision, error) {
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return datastore.Revision{}, err
	}

	item, err := schema.key(key, "")
	if err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: reading from %s: %w", d.ref, err,
		)
	}

	res, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key:       item,
	})
	if err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: reading from %s: %w", d.ref, err,
		)
	}

	if len(res.Item) == 0 {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: %s holds no item for %s: %w",
			d.ref, describeKey(key), datastore.ErrNotFound,
		)
	}

	revision := revisionOf(res.Item)
	delete(res.Item, datastore.RevisionField)
	if err := unmarshalItem(res.Item, out); err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: reading an item of %s into %T: %w", d.ref, out, err,
		)
	}
	return revision, nil
}

// Scan fills out with one page of the whole table, and returns the cursor to
// resume from.
//
// Every item is read whether or not a filter keeps it, which is the difference
// between this and [dynamoStore.Query] and the reason a query is what a handler
// should reach for where the data model allows one.
func (d *dynamoStore) Scan(
	ctx context.Context, scan datastore.Scan, out any,
) (datastore.Cursor, error) {
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return "", err
	}

	in, err := schema.scanInput(table, scan)
	if err != nil {
		return "", fmt.Errorf("celerity: scanning %s: %w", d.ref, err)
	}

	res, err := client.Scan(ctx, in)
	if err != nil {
		return "", fmt.Errorf("celerity: scanning %s: %w",
			d.ref, refusedCursor(err, scan.Cursor))
	}

	revisions := takeRevisions(res.Items)
	if err := unmarshalItems(res.Items, out); err != nil {
		return "", fmt.Errorf("celerity: reading the items of %s into %T: %w", d.ref, out, err)
	}
	datastore.DeliverRevisions(out, revisions)

	cursor, err := encodeCursor(res.LastEvaluatedKey)
	if err != nil {
		return "", fmt.Errorf("celerity: scanning %s: %w", d.ref, err)
	}
	return cursor, nil
}

// Reads the revision off each item and removes the attribute, so
// that the reserved field never reaches application code
// for whichever read produced the item.
func takeRevisions(items []map[string]dynamotypes.AttributeValue) []datastore.Revision {
	revisions := make([]datastore.Revision, len(items))
	for i, item := range items {
		revisions[i] = revisionOf(item)
		delete(item, datastore.RevisionField)
	}
	return revisions
}

// Reads the revision an earlier write stamped on an item. An item
// written by anything other than a Celerity SDK carries none, and reads back as
// [datastore.Unrevisioned].
func revisionOf(item map[string]dynamotypes.AttributeValue) datastore.Revision {
	stored, ok := item[datastore.RevisionField].(*dynamotypes.AttributeValueMemberS)
	if !ok {
		return datastore.Unrevisioned
	}
	return datastore.NewRevision(stored.Value)
}

// Put writes an item, replacing whatever was under the key.
//
// The key is given separately from the item and written over it, so that an
// item whose struct does not carry the key attributes is still addressable.
// Where it does carry them, a value that disagrees with the key is refused: a
// put replaces what is under a key rather than moving an item between keys, so
// overwriting silently would discard the change and report that the write
// succeeded. A field the item leaves empty is not a disagreement, which is what
// a read fills in and what read-modify-write writes back.
//
// A condition is what makes a read-then-write safe, and is refused with
// [datastore.ErrConditionFailed] rather than as an AWS error, so that a handler
// retrying does not have to know which store it is talking to.
func (d *dynamoStore) Put(
	ctx context.Context, key datastore.Key, item any, opts ...datastore.WriteOption,
) (datastore.Revision, error) {
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return datastore.Revision{}, err
	}

	attrs, err := marshalItem(item)
	if err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: writing a %T to %s: %w", item, d.ref, err)
	}
	keyAttrs, err := schema.key(key, "")
	if err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: writing to %s: %w", d.ref, err)
	}
	if err := applyKey(attrs, keyAttrs); err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: writing to %s: %w", d.ref, err)
	}

	written := nextRevision()
	attrs[datastore.RevisionField] = &dynamotypes.AttributeValueMemberS{Value: written}

	in := &dynamodb.PutItemInput{TableName: aws.String(table), Item: attrs}
	expr, err := writeExpression(datastore.ResolveWriteOptions(opts))
	if err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: writing to %s: %w", d.ref, err)
	}
	if expr != nil {
		in.ConditionExpression = expr.Condition()
		in.ExpressionAttributeNames = expr.Names()
		in.ExpressionAttributeValues = expr.Values()
	}

	if _, err := client.PutItem(ctx, in); err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: writing to %s: %w", d.ref, refused(err))
	}
	return datastore.NewRevision(written), nil
}

// nextRevision is the value a write stamps on an item.
//
// Random rather than a counter, because a counter would have to be read before
// it could be incremented, and not reading is the point of a revision
// precondition. crypto/rand.Read fills the buffer or panics, so there is no
// error to handle.
func nextRevision() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:])
}

// Delete removes an item, and is idempotent: DynamoDB answers a delete of a key
// that is not there the same way it answers one that was. A condition makes it
// conditional, and then a key that is not there can be a refusal.
func (d *dynamoStore) Delete(
	ctx context.Context, key datastore.Key, opts ...datastore.WriteOption,
) error {
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return err
	}
	keyAttrs, err := schema.key(key, "")
	if err != nil {
		return fmt.Errorf("celerity: deleting from %s: %w", d.ref, err)
	}

	in := &dynamodb.DeleteItemInput{
		TableName: aws.String(table),
		Key:       keyAttrs,
	}
	expr, err := writeExpression(datastore.ResolveWriteOptions(opts))
	if err != nil {
		return fmt.Errorf("celerity: deleting from %s: %w", d.ref, err)
	}
	if expr != nil {
		in.ConditionExpression = expr.Condition()
		in.ExpressionAttributeNames = expr.Names()
		in.ExpressionAttributeValues = expr.Values()
	}

	if _, err := client.DeleteItem(ctx, in); err != nil {
		return fmt.Errorf("celerity: deleting from %s: %w", d.ref, refused(err))
	}
	return nil
}

// Query returns one page of the items under a partition, and the cursor to
// resume from.
//
// One page rather than the whole partition, because a handler paging for a
// caller needs somewhere to stop and something to hand back, and a handler that
// wants the lot has [datastore.Items] to read it through. A partition can be
// large enough that reading it whole is not a thing to do by accident.
//
// The cursor is DynamoDB's own position, encoded. It carries the table's key as
// well as the index's when a secondary index is being queried, which is what
// makes resuming one possible: the two together are what DynamoDB requires, and
// neither the handler nor this package has to know that.
func (d *dynamoStore) Query(
	ctx context.Context, q datastore.Query, out any,
) (datastore.Cursor, error) {
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return "", err
	}

	in, err := schema.queryInput(table, q)
	if err != nil {
		return "", fmt.Errorf("celerity: querying %s: %w", d.ref, err)
	}

	res, err := client.Query(ctx, in)
	if err != nil {
		return "", fmt.Errorf("celerity: querying %s: %w",
			d.ref, refusedCursor(err, q.Cursor))
	}

	revisions := takeRevisions(res.Items)
	if err := unmarshalItems(res.Items, out); err != nil {
		return "", fmt.Errorf("celerity: reading the items of %s into %T: %w", d.ref, out, err)
	}
	datastore.DeliverRevisions(out, revisions)

	cursor, err := encodeCursor(res.LastEvaluatedKey)
	if err != nil {
		return "", fmt.Errorf("celerity: querying %s: %w", d.ref, err)
	}
	return cursor, nil
}

func (d *dynamoStore) resolve(ctx context.Context) (API, string, *tableSchema, error) {
	table, err := d.ref.ID(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	key, err := service.KeyFor(ctx, d.ref)
	if err != nil {
		return nil, "", nil, err
	}
	client, err := d.stores.dynamo(ctx, key)
	if err != nil {
		return nil, "", nil, err
	}
	schema, err := d.tableSchema(ctx, client, table)
	if err != nil {
		return nil, "", nil, err
	}
	return client, table, schema, nil
}

// Reads the table's key schema once and holds it.
//
// A table's keys are decided when it is created and cannot change, so this is
// read once per process rather than per call. This is one extra request on the first
// use of a table.
func (d *dynamoStore) tableSchema(
	ctx context.Context, client API, table string,
) (*tableSchema, error) {
	d.schemaOnce.Do(func() {
		out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
			TableName: aws.String(table),
		})
		if err != nil {
			d.schemaErr = fmt.Errorf(
				"celerity: reading the key schema of %s: %w", d.ref, err)
			return
		}
		d.schema = schemaOf(out.Table)
	})
	return d.schema, d.schemaErr
}

func describeKey(key datastore.Key) string {
	if key.Sort == "" {
		return fmt.Sprintf("%q", key.Partition)
	}
	return fmt.Sprintf("%q/%q", key.Partition, key.Sort)
}
