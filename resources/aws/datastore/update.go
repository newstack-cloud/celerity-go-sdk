package datastore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// Update mutates parts of an item and returns its new revision.
//
// DynamoDB's UpdateItem is an upsert: given a key that holds nothing it creates
// the item. Firestore and Cosmos DB refuse, and the portable contract is the
// stricter of the two, so an existence precondition goes on every update here.
// Without it the same handler would create a half-populated item on one store
// and report not-found on another, which wouldn't be noticed until it ran
// somewhere else.
func (d *dynamoStore) Update(
	ctx context.Context, key datastore.Key, updates []datastore.Update,
	opts ...datastore.WriteOption,
) (datastore.Revision, error) {
	if len(updates) == 0 {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: updating %s: an update changes nothing: %w",
			d.ref, datastore.ErrInvalidUpdate)
	}

	if len(updates) > datastore.MaxUpdates {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: updating %s with %d changes, and %d is the most any of these stores takes: %w",
			d.ref, len(updates), datastore.MaxUpdates, datastore.ErrTooManyOperations)
	}

	client, table, schema, err := d.resolve(ctx)
	if err != nil {
		return datastore.Revision{}, err
	}
	keyAttrs, err := schema.key(key, "")
	if err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: updating %s: %w", d.ref, err)
	}

	written := nextRevision()
	built, err := updateExpression(schema, updates, written, datastore.ResolveWriteOptions(opts))
	if err != nil {
		return datastore.Revision{}, fmt.Errorf("celerity: updating %s: %w", d.ref, err)
	}

	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(table),
		Key:                       keyAttrs,
		UpdateExpression:          built.Update(),
		ConditionExpression:       built.Condition(),
		ExpressionAttributeNames:  built.Names(),
		ExpressionAttributeValues: built.Values(),
		// So that a refusal can say whether the item was there. Without it,
		// "no such item" and "your condition did not hold" are the same error.
		ReturnValuesOnConditionCheckFailure: dynamotypes.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: updating %s: %w", d.ref, d.refusedUpdate(err, key))
	}
	return datastore.NewRevision(written), nil
}

// Says which of the two preconditions an update failed.
//
// Every update carries an existence check as well as whatever the caller asked
// for, so a refusal is ambiguous on its own. DynamoDB returns the old item with
// the refusal when there was one, so its absence is what distinguishes an item
// that was never there from a condition that did not hold.
func (d *dynamoStore) refusedUpdate(err error, key datastore.Key) error {
	var failed *dynamotypes.ConditionalCheckFailedException
	if !errors.As(err, &failed) {
		return err
	}
	if len(failed.Item) == 0 {
		return fmt.Errorf("%s holds no item for %s: %w",
			d.ref, describeKey(key), datastore.ErrNotFound)
	}
	return fmt.Errorf("%w: %w", datastore.ErrConditionFailed, err)
}

// Builds the mutations, the revision the write stamps, and the
// preconditions, as one expression so that the builder allocates every
// placeholder itself.
func updateExpression(
	schema *tableSchema, updates []datastore.Update,
	revision string, opts datastore.WriteOptions,
) (expression.Expression, error) {
	mutations, err := mutations(updates)
	if err != nil {
		return expression.Expression{}, err
	}
	// Stamped here rather than by a separate write, so that an item mutated in
	// place is revisioned exactly as one written whole is.
	mutations = mutations.Set(
		expression.Name(datastore.RevisionField),
		expression.Value(revision),
	)

	conditions := []expression.ConditionBuilder{
		expression.AttributeExists(
			expression.Name(schema.table.partition.name),
		),
	}
	if opts.Condition != nil {
		built, err := writeCondition(*opts.Condition)
		if err != nil {
			return expression.Expression{}, err
		}
		conditions = append(conditions, built)
	}

	if opts.Revision != nil {
		built, err := revisionCondition(*opts.Revision)
		if err != nil {
			return expression.Expression{}, err
		}
		conditions = append(conditions, built)
	}

	combined := conditions[0]
	for _, condition := range conditions[1:] {
		combined = combined.And(condition)
	}
	return expression.NewBuilder().WithUpdate(mutations).WithCondition(combined).Build()
}

func mutations(updates []datastore.Update) (expression.UpdateBuilder, error) {
	var built expression.UpdateBuilder
	for _, update := range updates {
		name, err := path(update.Path)
		if err != nil {
			return built, err
		}
		switch update.Kind {
		case datastore.UpdateSet:
			built = built.Set(name, expression.Value(update.Value))
		case datastore.UpdateRemove:
			built = built.Remove(name)
		case datastore.UpdateIncrement:
			// A field the item does not have counts as zero, which is what
			// makes this a counter rather than a read-then-add.
			built = built.Set(name, expression.Plus(
				expression.IfNotExists(name, expression.Value(0)),
				expression.Value(update.By),
			))
		default:
			return built, fmt.Errorf("%q is not a change any of these stores makes: %w",
				update.Kind, datastore.ErrInvalidUpdate)
		}
	}
	return built, nil
}

// path turns a dotted path into the nested name DynamoDB addresses.
//
// An empty segment is refused rather than sent, since DynamoDB would answer a
// malformed expression and the caller would have to work out which of their
// paths produced it.
func path(dotted string) (expression.NameBuilder, error) {
	if dotted == "" {
		return expression.NameBuilder{}, fmt.Errorf(
			"an update names no field: %w", datastore.ErrInvalidUpdate)
	}
	for _, segment := range strings.Split(dotted, ".") {
		if segment == "" {
			return expression.NameBuilder{}, fmt.Errorf(
				"the path %q has an empty segment: %w", dotted, datastore.ErrInvalidUpdate)
		}
	}
	return expression.Name(dotted), nil
}
