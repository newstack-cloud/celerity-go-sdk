package datastore

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// Atomically applies every operation or none of them.
//
// DynamoDB would take a wider transaction than this: any partition, any table,
// and reads through a separate call. The contract is the narrowest of the
// stores rather than the widest, so the partition is checked here and a write
// that DynamoDB would have applied is refused when Cosmos DB could not have.
func (d *dynamoStore) Atomically(
	ctx context.Context,
	partition string,
	ops []datastore.AtomicOp,
) error {
	if len(ops) == 0 {
		return nil
	}

	if len(ops) > datastore.MaxAtomicOps {
		return fmt.Errorf(
			"celerity: writing %d operations to %s atomically, and %d is the most any of "+
				"these stores takes: %w",
			len(ops), d.ref, datastore.MaxAtomicOps, datastore.ErrTooManyOperations)
	}

	for _, op := range ops {
		if op.Key.Partition != partition {
			return fmt.Errorf(
				"celerity: writing to %s atomically in partition %q, and an operation "+
					"addresses %q: %w",
				d.ref, partition, op.Key.Partition, datastore.ErrWrongPartition)
		}
	}

	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return err
	}

	items := make([]dynamotypes.TransactWriteItem, 0, len(ops))
	for _, op := range ops {
		item, err := d.transactItem(table, schema, op)
		if err != nil {
			return fmt.Errorf("celerity: writing to %s atomically: %w", d.ref, err)
		}
		items = append(items, item)
	}

	_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	if err != nil {
		return fmt.Errorf("celerity: writing to %s atomically: %w", d.ref, cancelled(err))
	}

	return nil
}

func (d *dynamoStore) transactItem(
	table string, schema *tableSchema, op datastore.AtomicOp,
) (dynamotypes.TransactWriteItem, error) {
	keyAttrs, err := schema.key(op.Key, "")
	if err != nil {
		return dynamotypes.TransactWriteItem{}, err
	}

	switch {
	case op.IsDelete():
		return deleteItem(table, keyAttrs, op)
	case op.IsUpdate():
		return updateItem(table, keyAttrs, schema, op)
	default:
		return putItem(table, keyAttrs, op)
	}
}

func putItem(
	table string,
	keyAttrs map[string]dynamotypes.AttributeValue,
	op datastore.AtomicOp,
) (dynamotypes.TransactWriteItem, error) {
	attrs, err := marshalItem(op.Item)
	if err != nil {
		return dynamotypes.TransactWriteItem{}, fmt.Errorf(
			"writing a %T: %w", op.Item, err,
		)
	}

	if err := applyKey(attrs, keyAttrs); err != nil {
		return dynamotypes.TransactWriteItem{}, err
	}

	attrs[datastore.RevisionField] = &dynamotypes.AttributeValueMemberS{
		Value: nextRevision(),
	}

	put := &dynamotypes.Put{
		TableName: aws.String(table),
		Item:      attrs,
	}
	expr, err := writeExpression(op.Options)
	if err != nil {
		return dynamotypes.TransactWriteItem{}, err
	}

	if expr != nil {
		put.ConditionExpression = expr.Condition()
		put.ExpressionAttributeNames = expr.Names()
		put.ExpressionAttributeValues = expr.Values()
	}

	return dynamotypes.TransactWriteItem{Put: put}, nil
}

func updateItem(
	table string,
	keyAttrs map[string]dynamotypes.AttributeValue,
	schema *tableSchema,
	op datastore.AtomicOp,
) (dynamotypes.TransactWriteItem, error) {
	if len(op.Updates) == 0 {
		return dynamotypes.TransactWriteItem{}, fmt.Errorf(
			"an update changes nothing: %w", datastore.ErrInvalidUpdate)
	}

	if len(op.Updates) > datastore.MaxUpdates {
		return dynamotypes.TransactWriteItem{}, fmt.Errorf(
			"an update carries %d changes, and %d is the most any of the backing stores takes: %w",
			len(op.Updates), datastore.MaxUpdates, datastore.ErrTooManyOperations)
	}

	built, err := updateExpression(schema, op.Updates, nextRevision(), op.Options)
	if err != nil {
		return dynamotypes.TransactWriteItem{}, err
	}

	return dynamotypes.TransactWriteItem{
		Update: &dynamotypes.Update{
			TableName:                 aws.String(table),
			Key:                       keyAttrs,
			UpdateExpression:          built.Update(),
			ConditionExpression:       built.Condition(),
			ExpressionAttributeNames:  built.Names(),
			ExpressionAttributeValues: built.Values(),
		},
	}, nil
}

func deleteItem(
	table string,
	keyAttrs map[string]dynamotypes.AttributeValue,
	op datastore.AtomicOp,
) (dynamotypes.TransactWriteItem, error) {
	remove := &dynamotypes.Delete{
		TableName: aws.String(table),
		Key:       keyAttrs,
	}
	expr, err := writeExpression(op.Options)
	if err != nil {
		return dynamotypes.TransactWriteItem{}, err
	}

	if expr != nil {
		remove.ConditionExpression = expr.Condition()
		remove.ExpressionAttributeNames = expr.Names()
		remove.ExpressionAttributeValues = expr.Values()
	}
	return dynamotypes.TransactWriteItem{Delete: remove}, nil
}

// Turns a cancelled transaction into the sentinel a handler tests for
// when it is one of the preconditions that did not hold.
//
// DynamoDB reports the whole transaction as cancelled and says why per
// operation, so the reasons are what distinguishes a refused precondition from
// a conflict or a capacity problem, which a handler answers differently.
func cancelled(err error) error {
	var canceled *dynamotypes.TransactionCanceledException
	if !errors.As(err, &canceled) {
		return err
	}

	for _, reason := range canceled.CancellationReasons {
		if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
			return fmt.Errorf("%w: %w", datastore.ErrConditionFailed, err)
		}
	}

	return err
}
