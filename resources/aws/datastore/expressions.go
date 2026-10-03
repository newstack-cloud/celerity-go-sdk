package datastore

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// Conditions are translated into DynamoDB's own expression builder rather than
// written out as strings.
//
// An expression carries its attribute names and values as placeholders, and
// allocating those by hand is how an attribute named after a reserved word, or a
// value holding a colon, becomes a malformed expression. The builder also gets
// the precedence of nested and-or right without additional logic in directly
// generating strings from Celerity datastore conditions.

// keyCondition builds the condition addressing part of a partition.
func keyCondition(schema keySchema, q datastore.Query) (expression.KeyConditionBuilder, error) {
	partition := expression.Key(schema.partition.name).
		Equal(expression.Value(value(q.Partition, schema.partition.kind)))

	if q.Sort == nil {
		return partition, nil
	}

	if schema.sort.name == "" {
		return expression.KeyConditionBuilder{}, fmt.Errorf(
			"the query narrows by sort value, and the key is %q alone",
			schema.partition.name,
		)
	}

	sort, err := sortCondition(schema.sort, *q.Sort)
	if err != nil {
		return expression.KeyConditionBuilder{}, err
	}
	return partition.And(sort), nil
}

// Narrows a query, on the attribute the table calls its sort key.
//
// The value goes through the declared type the same way a key value does, since
// a sort key declared as a number and compared against a string is refused.
func sortCondition(
	attr attribute, condition datastore.SortCondition,
) (expression.KeyConditionBuilder, error) {
	key := expression.Key(attr.name)
	low := expression.Value(value(condition.Value, attr.kind))

	switch condition.Op {
	case datastore.OpEqual:
		return key.Equal(low), nil
	case datastore.OpLess:
		return key.LessThan(low), nil
	case datastore.OpLessOrEqual:
		return key.LessThanEqual(low), nil
	case datastore.OpGreater:
		return key.GreaterThan(low), nil
	case datastore.OpGreaterOrEqual:
		return key.GreaterThanEqual(low), nil
	case datastore.OpBetween:
		return key.Between(low, expression.Value(value(condition.High, attr.kind))), nil
	case datastore.OpStartsWith:
		return key.BeginsWith(condition.Value), nil
	default:
		return expression.KeyConditionBuilder{}, fmt.Errorf(
			"a sort key cannot be narrowed by %q: it is ordered, and only equal, the four "+
				"comparisons, between and starts-with use that order: %w",
			condition.Op, datastore.ErrInvalidCondition)
	}
}

// Builds the condition a conditional write is refused unless it
// holds.
//
// The attributes here are the item's own rather than the key's, so there is no
// declared type to go through, the value is marshalled as whatever Go type it
// is, which is how a number stays a number.
func writeCondition(condition datastore.Condition) (expression.ConditionBuilder, error) {
	switch condition.Op {
	case datastore.OpAnd, datastore.OpOr:
		return group(condition)
	case datastore.OpExists:
		return expression.AttributeExists(expression.Name(condition.Name)), nil
	case datastore.OpNotExists:
		return expression.AttributeNotExists(expression.Name(condition.Name)), nil
	}

	name := expression.Name(condition.Name)
	operand := expression.Value(condition.Value)

	switch condition.Op {
	case datastore.OpEqual:
		return name.Equal(operand), nil
	case datastore.OpNotEqual:
		return name.NotEqual(operand), nil
	case datastore.OpLess:
		return name.LessThan(operand), nil
	case datastore.OpLessOrEqual:
		return name.LessThanEqual(operand), nil
	case datastore.OpGreater:
		return name.GreaterThan(operand), nil
	case datastore.OpGreaterOrEqual:
		return name.GreaterThanEqual(operand), nil
	case datastore.OpBetween:
		return name.Between(operand, expression.Value(condition.High)), nil
	case datastore.OpStartsWith:
		return name.BeginsWith(text(condition.Value)), nil
	case datastore.OpContains:
		return name.Contains(text(condition.Value)), nil
	default:
		return expression.ConditionBuilder{}, fmt.Errorf(
			"%q is not a condition this store can check, use the helper functions to build "+
				"supported conditions: %w",
			condition.Op, datastore.ErrInvalidCondition,
		)
	}
}

// Combines the conditions of an All or an Any.
//
// One condition combined with nothing is that condition, which is what a
// condition built up from a variable number of parts produces and is worth
// taking rather than refusing. None at all is refused: it would read as a write
// with no condition, which is the opposite of what asking for one means.
func group(condition datastore.Condition) (expression.ConditionBuilder, error) {
	if len(condition.Of) == 0 {
		return expression.ConditionBuilder{}, fmt.Errorf(
			"a %q combines conditions and was given none, which would read as a write "+
				"with no condition at all",
			condition.Op,
		)
	}

	built := make([]expression.ConditionBuilder, 0, len(condition.Of))
	for _, of := range condition.Of {
		one, err := writeCondition(of)
		if err != nil {
			return expression.ConditionBuilder{}, err
		}
		built = append(built, one)
	}

	if len(built) == 1 {
		return built[0], nil
	}

	if condition.Op == datastore.OpOr {
		return expression.Or(built[0], built[1], built[2:]...), nil
	}

	return expression.And(built[0], built[1], built[2:]...), nil
}

// text renders a value for the operators DynamoDB only accepts a string for.
func text(value any) string {
	if str, ok := value.(string); ok {
		return str
	}
	return fmt.Sprint(value)
}

// This is what IfUnchanged compiles to.
//
// An item carrying no revision is required to still carry none, which is what
// makes read-modify-write correct on items written before revisions existed
// without a migration: another Celerity write would have stamped one, so the
// condition fails and the caller re-reads.
func revisionCondition(revision datastore.Revision) (expression.ConditionBuilder, error) {
	if !revision.Known() {
		return expression.ConditionBuilder{}, datastore.ErrInvalidRevision
	}

	field := expression.Name(datastore.RevisionField)
	value, carried := revision.Value()
	if !carried {
		return expression.AttributeNotExists(field), nil
	}
	return field.Equal(expression.Value(value)), nil
}

// Builds the condition a write carries, which may come from the
// caller's own condition, from a revision precondition, or from both. Nil when
// the write is unconditional.
//
// The builder allocates the placeholders, so the names and values come back
// alongside the expression and all three go on the request together.
func writeExpression(opts datastore.WriteOptions) (*expression.Expression, error) {
	var conditions []expression.ConditionBuilder
	if opts.Condition != nil {
		built, err := writeCondition(*opts.Condition)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, built)
	}

	if opts.Revision != nil {
		built, err := revisionCondition(*opts.Revision)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, built)
	}

	if len(conditions) == 0 {
		return nil, nil
	}

	combined := conditions[0]
	for _, condition := range conditions[1:] {
		combined = combined.And(condition)
	}
	built, err := expression.NewBuilder().WithCondition(combined).Build()
	if err != nil {
		return nil, err
	}

	return &built, nil
}

// Turns DynamoDB's own way of saying a condition did not hold into the
// sentinel a handler tests for.
//
// Wrapped rather than replaced, so the AWS error is still there for anything
// that wants to look, and errors.Is finds the sentinel either way.
func refused(err error) error {
	var failed *dynamotypes.ConditionalCheckFailedException
	if errors.As(err, &failed) {
		return fmt.Errorf("%w: %w", datastore.ErrConditionFailed, err)
	}
	return err
}
