package datastore

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// What DynamoDB takes in one request. A batch larger than this is split rather
// than refused, since the limit is the service's and not something the data
// model asked for.
const (
	getsPerRequest   = 100
	writesPerRequest = 25
)

// BatchGet fills out with the items under the keys and returns the keys it
// could not fetch.
//
// DynamoDB answers a batch partly when it is throttled, which is what the
// returned keys are. A caller retries them, with backoff. Three of the five
// stores in the contract cannot provide a partial answer, so code that treats an
// empty list as proof the store never partly fails is reading a DynamoDB
// behaviour as a portable one.
func (d *dynamoStore) BatchGet(
	ctx context.Context, keys []datastore.Key, out any,
) ([]datastore.Key, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return nil, err
	}

	var items []map[string]dynamotypes.AttributeValue
	var unprocessed []datastore.Key
	for chunk := range slices.Chunk(keys, getsPerRequest) {
		read, missed, err := d.getChunk(ctx, client, table, schema, chunk)
		if err != nil {
			return nil, err
		}
		items = append(items, read...)
		unprocessed = append(unprocessed, missed...)
	}

	revisions := takeRevisions(items)
	if err := unmarshalItems(items, out); err != nil {
		return nil, fmt.Errorf("celerity: reading the items of %s into %T: %w", d.ref, out, err)
	}
	datastore.DeliverRevisions(out, revisions)
	return unprocessed, nil
}

func (d *dynamoStore) getChunk(
	ctx context.Context, client API, table string,
	schema *tableSchema, keys []datastore.Key,
) ([]map[string]dynamotypes.AttributeValue, []datastore.Key, error) {

	attrs := make([]map[string]dynamotypes.AttributeValue, 0, len(keys))
	for _, key := range keys {
		built, err := schema.key(key, "")
		if err != nil {
			return nil, nil, fmt.Errorf("celerity: reading from %s: %w", d.ref, err)
		}
		attrs = append(attrs, built)
	}

	res, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]dynamotypes.KeysAndAttributes{
			table: {Keys: attrs},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("celerity: reading from %s: %w", d.ref, err)
	}

	missed, err := d.keysOf(schema, res.UnprocessedKeys[table].Keys)
	if err != nil {
		return nil, nil, err
	}
	return res.Responses[table], missed, nil
}

// BatchWrite applies puts and deletes and returns the operations it could not
// apply.
//
// This is not atomic, DynamoDB applies what it can and reports the rest, so a caller
// retries what comes back. A put stamps a revision exactly as a single write
// does, so an item is not left unrevisioned by having been written in a batch.
//
// A DynamoDB WriteRequest is a put or a delete and nothing else, which is why
// there is no update here while [dynamoStore.Atomically] has one: a
// TransactWriteItem carries an Update and a WriteRequest has no equivalent.
func (d *dynamoStore) BatchWrite(
	ctx context.Context, ops []datastore.BatchOp,
) ([]datastore.BatchOp, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return nil, err
	}

	var unprocessed []datastore.BatchOp
	for chunk := range slices.Chunk(ops, writesPerRequest) {
		missed, err := d.writeChunk(ctx, client, table, schema, chunk)
		if err != nil {
			return nil, err
		}
		unprocessed = append(unprocessed, missed...)
	}
	return unprocessed, nil
}

func (d *dynamoStore) writeChunk(
	ctx context.Context, client API, table string,
	schema *tableSchema, ops []datastore.BatchOp,
) ([]datastore.BatchOp, error) {
	requests := make([]dynamotypes.WriteRequest, 0, len(ops))
	for _, op := range ops {
		request, err := d.writeRequest(schema, op)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}

	res, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]dynamotypes.WriteRequest{table: requests},
	})
	if err != nil {
		return nil, fmt.Errorf("celerity: writing to %s: %w", d.ref, err)
	}
	return d.opsFor(schema, ops, res.UnprocessedItems[table])
}

func (d *dynamoStore) writeRequest(
	schema *tableSchema, op datastore.BatchOp,
) (dynamotypes.WriteRequest, error) {
	keyAttrs, err := schema.key(op.Key, "")
	if err != nil {
		return dynamotypes.WriteRequest{}, fmt.Errorf(
			"celerity: writing to %s: %w", d.ref, err,
		)
	}

	if op.IsDelete() {
		return dynamotypes.WriteRequest{
			DeleteRequest: &dynamotypes.DeleteRequest{Key: keyAttrs},
		}, nil
	}

	attrs, err := marshalItem(op.Item)
	if err != nil {
		return dynamotypes.WriteRequest{}, fmt.Errorf(
			"celerity: writing a %T to %s: %w", op.Item, d.ref, err,
		)
	}

	if err := applyKey(attrs, keyAttrs); err != nil {
		return dynamotypes.WriteRequest{}, fmt.Errorf(
			"celerity: writing to %s: %w", d.ref, err,
		)
	}
	attrs[datastore.RevisionField] = &dynamotypes.AttributeValueMemberS{
		Value: nextRevision(),
	}
	return dynamotypes.WriteRequest{
		PutRequest: &dynamotypes.PutRequest{Item: attrs},
	}, nil
}

// Matches what DynamoDB could not apply back to the operations the
// caller gave, so that a retry is of the caller's own values rather than of
// attribute maps this package built.
func (d *dynamoStore) opsFor(
	schema *tableSchema, ops []datastore.BatchOp, unprocessed []dynamotypes.WriteRequest,
) ([]datastore.BatchOp, error) {
	if len(unprocessed) == 0 {
		return nil, nil
	}

	byKey := make(map[datastore.Key]datastore.BatchOp, len(ops))
	for _, op := range ops {
		byKey[op.Key] = op
	}

	missed := make([]datastore.BatchOp, 0, len(unprocessed))
	for _, request := range unprocessed {
		attrs := requestKey(request)
		key, err := d.keyOf(schema, attrs)
		if err != nil {
			return nil, err
		}
		if op, ok := byKey[key]; ok {
			missed = append(missed, op)
		}
	}
	return missed, nil
}

func requestKey(request dynamotypes.WriteRequest) map[string]dynamotypes.AttributeValue {
	if request.DeleteRequest != nil {
		return request.DeleteRequest.Key
	}

	if request.PutRequest != nil {
		return request.PutRequest.Item
	}

	return nil
}

func (d *dynamoStore) keysOf(
	schema *tableSchema, attrs []map[string]dynamotypes.AttributeValue,
) ([]datastore.Key, error) {
	if len(attrs) == 0 {
		return nil, nil
	}

	keys := make([]datastore.Key, 0, len(attrs))
	for _, attr := range attrs {
		key, err := d.keyOf(schema, attr)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// Reads a key back out of the attributes a request carried, which is the
// only way to say which of the caller's keys DynamoDB did not get to.
func (d *dynamoStore) keyOf(
	schema *tableSchema, attrs map[string]dynamotypes.AttributeValue,
) (datastore.Key, error) {
	if attrs == nil {
		return datastore.Key{}, fmt.Errorf(
			"celerity: %s answered with a request carrying no key", d.ref)
	}

	return datastore.Key{
		Partition: keyText(attrs[schema.table.partition.name]),
		Sort:      keyText(attrs[schema.table.sort.name]),
	}, nil
}

// The inverse of value, a key is a string in the provider-agnostic
// interface whatever the table declared it as.
func keyText(attr dynamotypes.AttributeValue) string {
	switch typed := attr.(type) {
	case *dynamotypes.AttributeValueMemberS:
		return typed.Value
	case *dynamotypes.AttributeValueMemberN:
		return typed.Value
	case *dynamotypes.AttributeValueMemberB:
		return string(typed.Value)
	default:
		return ""
	}
}
