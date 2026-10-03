package datastore

import (
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// tableSchema is what a table calls its keys, and what types they hold.
//
// The whole of what this package needs DescribeTable for. A handler addresses
// an item by partition and sort value, the two things every document store has;
// turning those into a DynamoDB key needs the attribute names, and turning a
// value into an attribute needs the declared type, since a key attribute
// declared as a number and sent as a string is refused.
type tableSchema struct {
	table   keySchema
	indexes map[string]keySchema
}

type keySchema struct {
	partition attribute
	// sort is the attribute of a composite key, and is empty where the key is
	// the partition alone.
	sort attribute
}

type attribute struct {
	name string
	kind dynamotypes.ScalarAttributeType
}

func schemaOf(table *dynamotypes.TableDescription) *tableSchema {
	if table == nil {
		return &tableSchema{indexes: map[string]keySchema{}}
	}

	// Declared once for the whole table and referred to by name from every
	// index, so the types are collected first.
	kinds := make(map[string]dynamotypes.ScalarAttributeType, len(table.AttributeDefinitions))
	for _, def := range table.AttributeDefinitions {
		kinds[aws.ToString(def.AttributeName)] = def.AttributeType
	}

	schema := &tableSchema{
		table:   keySchemaOf(table.KeySchema, kinds),
		indexes: make(map[string]keySchema),
	}
	for _, index := range table.GlobalSecondaryIndexes {
		schema.indexes[aws.ToString(index.IndexName)] = keySchemaOf(index.KeySchema, kinds)
	}
	for _, index := range table.LocalSecondaryIndexes {
		schema.indexes[aws.ToString(index.IndexName)] = keySchemaOf(index.KeySchema, kinds)
	}
	return schema
}

func keySchemaOf(
	elements []dynamotypes.KeySchemaElement,
	kinds map[string]dynamotypes.ScalarAttributeType,
) keySchema {
	var schema keySchema
	for _, element := range elements {
		name := aws.ToString(element.AttributeName)
		attr := attribute{name: name, kind: kinds[name]}
		if element.KeyType == dynamotypes.KeyTypeHash {
			schema.partition = attr
			continue
		}
		schema.sort = attr
	}
	return schema
}

// key turns a handler's key into the attributes DynamoDB addresses an item by,
// against the table's own key schema or one of its indexes.
func (s *tableSchema) key(key datastore.Key, index string) (map[string]dynamotypes.AttributeValue, error) {
	schema, err := s.of(index)
	if err != nil {
		return nil, err
	}
	if schema.partition.name == "" {
		return nil, fmt.Errorf("the table declares no partition key")
	}

	attrs := map[string]dynamotypes.AttributeValue{
		schema.partition.name: value(key.Partition, schema.partition.kind),
	}

	switch {
	case schema.sort.name == "" && key.Sort != "":
		return nil, fmt.Errorf(
			"a sort value %q was given, and the key is %q alone",
			key.Sort, schema.partition.name,
		)
	case schema.sort.name != "" && key.Sort == "":
		return nil, fmt.Errorf(
			"the key is %q and %q, and no sort value was given",
			schema.partition.name, schema.sort.name,
		)
	case schema.sort.name != "":
		attrs[schema.sort.name] = value(key.Sort, schema.sort.kind)
	}
	return attrs, nil
}

func (s *tableSchema) of(index string) (keySchema, error) {
	if index == "" {
		return s.table, nil
	}
	schema, ok := s.indexes[index]
	if !ok {
		return keySchema{}, fmt.Errorf("the table has no index named %q", index)
	}
	return schema, nil
}

// queryInput builds the request for the items under one partition.
func (s *tableSchema) queryInput(table string, q datastore.Query) (*dynamodb.QueryInput, error) {
	schema, err := s.of(q.Index)
	if err != nil {
		return nil, err
	}
	if schema.partition.name == "" {
		return nil, fmt.Errorf("the table declares no partition key")
	}

	key, err := keyCondition(schema, q)
	if err != nil {
		return nil, err
	}
	builder := expression.NewBuilder().WithKeyCondition(key)
	if q.Filter != nil {
		filter, err := writeCondition(*q.Filter)
		if err != nil {
			return nil, err
		}
		builder = builder.WithFilter(filter)
	}
	if len(q.Project) > 0 {
		builder = builder.WithProjection(s.projection(schema, q.Project))
	}
	built, err := builder.Build()
	if err != nil {
		return nil, err
	}

	in := &dynamodb.QueryInput{
		TableName:                 aws.String(table),
		KeyConditionExpression:    built.KeyCondition(),
		FilterExpression:          built.Filter(),
		ProjectionExpression:      built.Projection(),
		ExpressionAttributeNames:  built.Names(),
		ExpressionAttributeValues: built.Values(),
	}
	if q.Index != "" {
		in.IndexName = aws.String(q.Index)
	}
	if q.Descending {
		in.ScanIndexForward = aws.Bool(false)
	}

	// Where an earlier page stopped, exactly as DynamoDB gave it. On a
	// secondary index that is the index's key and the table's together, which
	// is what the service requires and what nothing else could reconstruct.
	start, err := decodeCursor(q.Cursor)
	if err != nil {
		return nil, err
	}
	in.ExclusiveStartKey = start

	if q.Limit > 0 {
		in.Limit = aws.Int32(int32(q.Limit))
	}
	return in, nil
}

// scanInput builds the request for a read of the whole table.
//
// No key condition, since there is no partition, and no index, since scanning a
// named index is not in the portable contract. What is left is what a query
// also carries.
func (s *tableSchema) scanInput(table string, scan datastore.Scan) (*dynamodb.ScanInput, error) {
	builder := expression.NewBuilder()
	building := false
	if scan.Filter != nil {
		filter, err := writeCondition(*scan.Filter)
		if err != nil {
			return nil, err
		}
		builder, building = builder.WithFilter(filter), true
	}
	if len(scan.Project) > 0 {
		builder, building = builder.WithProjection(s.projection(s.table, scan.Project)), true
	}

	in := &dynamodb.ScanInput{TableName: aws.String(table)}
	if building {
		built, err := builder.Build()
		if err != nil {
			return nil, err
		}
		in.FilterExpression = built.Filter()
		in.ProjectionExpression = built.Projection()
		in.ExpressionAttributeNames = built.Names()
		in.ExpressionAttributeValues = built.Values()
	}

	start, err := decodeCursor(scan.Cursor)
	if err != nil {
		return nil, err
	}
	in.ExclusiveStartKey = start

	if scan.Limit > 0 {
		in.Limit = aws.Int32(int32(scan.Limit))
	}
	return in, nil
}

// projection is the fields a query returns, plus the ones it cannot leave out.
//
// The key fields go in whether or not the caller listed them, since an item
// that cannot be identified is not much of an answer, and the revision goes in
// so that a projected read still produces one a later write can require. Both
// are what the caller would have had to remember otherwise.
func (s *tableSchema) projection(schema keySchema, fields []string) expression.ProjectionBuilder {
	wanted := make([]string, 0, len(fields)+3)
	wanted = append(wanted, fields...)
	for _, required := range []string{
		schema.partition.name, schema.sort.name,
		s.table.partition.name, s.table.sort.name,
		datastore.RevisionField,
	} {
		if required != "" && !slices.Contains(wanted, required) {
			wanted = append(wanted, required)
		}
	}

	names := make([]expression.NameBuilder, len(wanted))
	for i, name := range wanted {
		names[i] = expression.Name(name)
	}
	return expression.ProjectionBuilder{}.AddNames(names...)
}

// value turns a key value into the attribute the table declared for it.
//
// A key is a string in the provider-agnostic interface, because every document
// store has partition and sort values and only some of them have types. What
// DynamoDB declared decides how it is sent, so a table keyed on a number is
// addressed with a number rather than being refused.
func value(raw string, kind dynamotypes.ScalarAttributeType) dynamotypes.AttributeValue {
	switch kind {
	case dynamotypes.ScalarAttributeTypeN:
		return &dynamotypes.AttributeValueMemberN{Value: raw}
	case dynamotypes.ScalarAttributeTypeB:
		return &dynamotypes.AttributeValueMemberB{Value: []byte(raw)}
	default:
		return &dynamotypes.AttributeValueMemberS{Value: raw}
	}
}
