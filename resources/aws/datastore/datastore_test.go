package datastore_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsdatastore "github.com/newstack-cloud/celerity-go-sdk/resources/aws/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

type DatastoreTestSuite struct {
	suite.Suite
}

func TestDatastoreTestSuite(t *testing.T) {
	suite.Run(t, new(DatastoreTestSuite))
}

type order struct {
	CustomerID string `dynamodbav:"customerId"`
	OrderID    string `dynamodbav:"orderId"`
	Total      int    `dynamodbav:"total"`
}

type fakeDynamo struct {
	described int
	table     *dynamotypes.TableDescription

	got        *dynamodb.GetItemInput
	put        *dynamodb.PutItemInput
	deleted    *dynamodb.DeleteItemInput
	updated    *dynamodb.UpdateItemInput
	transacted *dynamodb.TransactWriteItemsInput
	queried    []*dynamodb.QueryInput
	scanned    []*dynamodb.ScanInput
	scans      []*dynamodb.ScanOutput
	batchGot   []*dynamodb.BatchGetItemInput
	gets       []*dynamodb.BatchGetItemOutput
	batchPut   []*dynamodb.BatchWriteItemInput
	writes     []*dynamodb.BatchWriteItemOutput
	item       map[string]dynamotypes.AttributeValue
	pages      []*dynamodb.QueryOutput
	err        error
	describe   error
}

func (f *fakeDynamo) DescribeTable(
	_ context.Context, _ *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options),
) (*dynamodb.DescribeTableOutput, error) {
	f.described += 1
	if f.describe != nil {
		return nil, f.describe
	}
	return &dynamodb.DescribeTableOutput{Table: f.table}, nil
}

func (f *fakeDynamo) GetItem(
	_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.GetItemOutput{Item: f.item}, nil
}

func (f *fakeDynamo) PutItem(
	_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	f.put = in
	return &dynamodb.PutItemOutput{}, f.err
}

func (f *fakeDynamo) DeleteItem(
	_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.deleted = in
	return &dynamodb.DeleteItemOutput{}, f.err
}

func (f *fakeDynamo) Query(
	_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	f.queried = append(f.queried, in)
	if f.err != nil {
		return nil, f.err
	}
	return f.pages[min(len(f.queried)-1, len(f.pages)-1)], nil
}

func (f *fakeDynamo) Scan(
	_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options),
) (*dynamodb.ScanOutput, error) {
	f.scanned = append(f.scanned, in)
	if f.err != nil {
		return nil, f.err
	}
	page := f.scans[min(len(f.scanned)-1, len(f.scans)-1)]
	return page, nil
}

func (f *fakeDynamo) BatchGetItem(
	_ context.Context, in *dynamodb.BatchGetItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.BatchGetItemOutput, error) {
	f.batchGot = append(f.batchGot, in)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.gets) == 0 {
		return &dynamodb.BatchGetItemOutput{}, nil
	}
	return f.gets[min(len(f.batchGot)-1, len(f.gets)-1)], nil
}

func (f *fakeDynamo) BatchWriteItem(
	_ context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.BatchWriteItemOutput, error) {
	f.batchPut = append(f.batchPut, in)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.writes) == 0 {
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	return f.writes[min(len(f.batchPut)-1, len(f.writes)-1)], nil
}

func (f *fakeDynamo) UpdateItem(
	_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	f.updated = in
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeDynamo) TransactWriteItems(
	_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options),
) (*dynamodb.TransactWriteItemsOutput, error) {
	f.transacted = in
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

// compositeTable is keyed on a partition and a sort attribute, with an index.
func compositeTable() *dynamotypes.TableDescription {
	return &dynamotypes.TableDescription{
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("customerId"), AttributeType: dynamotypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("orderId"), AttributeType: dynamotypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("total"), AttributeType: dynamotypes.ScalarAttributeTypeN},
		},
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("customerId"), KeyType: dynamotypes.KeyTypeHash},
			{AttributeName: aws.String("orderId"), KeyType: dynamotypes.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []dynamotypes.GlobalSecondaryIndexDescription{{
			IndexName: aws.String("byTotal"),
			KeySchema: []dynamotypes.KeySchemaElement{
				{AttributeName: aws.String("total"), KeyType: dynamotypes.KeyTypeHash},
			},
		}},
	}
}

// partitionOnlyTable is keyed on a partition attribute alone.
func partitionOnlyTable() *dynamotypes.TableDescription {
	return &dynamotypes.TableDescription{
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("customerId"), AttributeType: dynamotypes.ScalarAttributeTypeS},
		},
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("customerId"), KeyType: dynamotypes.KeyTypeHash},
		},
	}
}

func (s *DatastoreTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

// datastoreOn is the store every datastore suite drives, so a suite does not
// have to know how a provider is put together to exercise one.
func datastoreOn(t *testing.T, api *fakeDynamo) datastore.Client {
	t.Helper()
	store, err := awsdatastore.Stores(api)(
		awstest.SimpleRef(resources.KindDatastore, "ordersTable", "orders-prod"),
	)
	require.NoError(t, err)
	return store
}

func (s *DatastoreTestSuite) Test_the_key_schema_is_read_once_however_many_calls_are_made() {
	// A table's keys are decided when it is created and cannot change, so this
	// is one extra request on first use rather than one per call.
	api := &fakeDynamo{table: compositeTable(), item: map[string]dynamotypes.AttributeValue{
		"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
	}}
	store := s.store(api)
	key := datastore.Key{Partition: "c-1", Sort: "o-1"}

	var got order
	_, err := store.Get(awstest.Ctx(), key, &got)
	s.Require().NoError(err)
	_, err = store.Get(awstest.Ctx(), key, &got)
	s.Require().NoError(err)
	s.Require().NoError(store.Delete(awstest.Ctx(), key))

	s.Equal(1, api.described)
}

func (s *DatastoreTestSuite) Test_a_key_value_is_sent_as_the_type_the_table_declared() {
	// A key attribute declared as a number and sent as a string is refused, and
	// the handler's key is a string because not every document store has types.
	api := &fakeDynamo{table: compositeTable(), pages: []*dynamodb.QueryOutput{{}}}

	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{
		Partition: "42",
		Index:     "byTotal",
	}, &[]order{})

	s.Require().NoError(err)
	// The placeholders an expression carries are allocated by the builder, so
	// what is checked is the attribute and the value it stands for rather than
	// what either is called.
	s.Contains(namesOf(api.queried[0]), "total")
	s.Contains(valuesOf(api.queried[0]), dynamotypes.AttributeValue(
		&dynamotypes.AttributeValueMemberN{Value: "42"}))
	s.Equal("byTotal", aws.ToString(api.queried[0].IndexName))
}

func (s *DatastoreTestSuite) Test_a_key_that_does_not_fit_the_table_is_refused() {
	cases := []struct {
		name  string
		table *dynamotypes.TableDescription
		key   datastore.Key
		says  string
	}{
		{
			name:  "no sort value for a composite key",
			table: compositeTable(),
			key:   datastore.Key{Partition: "c-1"},
			says:  "no sort value was given",
		},
		{
			// Sending it would address an item that is not the one asked for,
			// since DynamoDB ignores an attribute that is not in the key.
			name:  "a sort value for a key that has none",
			table: partitionOnlyTable(),
			key:   datastore.Key{Partition: "c-1", Sort: "o-1"},
			says:  "alone",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{table: tc.table}

			var got order
			_, err := s.store(api).Get(awstest.Ctx(), tc.key, &got)

			s.Require().Error(err)
			s.Contains(err.Error(), tc.says)
			s.Nil(api.got, "nothing should have been asked of DynamoDB")
		})
	}
}

func (s *DatastoreTestSuite) Test_an_index_the_table_does_not_have_is_refused() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Query(awstest.Ctx(),
		datastore.Query{Partition: "c-1", Index: "byDate"}, &[]order{})

	s.Require().Error(err)
	s.Contains(err.Error(), "byDate")
	s.Empty(api.queried)
}

func (s *DatastoreTestSuite) Test_a_cursor_from_somewhere_else_is_refused() {
	// A cursor reaches a handler from a client, so it is checked rather than
	// passed on.
	api := &fakeDynamo{table: compositeTable()}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(),
		datastore.Query{Partition: "c-1", Cursor: "not a cursor!!"}, &found)

	s.Require().ErrorIs(err, datastore.ErrInvalidCursor)
	s.Empty(api.queried, "nothing should have been asked of DynamoDB")
}

func (s *DatastoreTestSuite) Test_a_cursor_the_store_will_not_resume_from_is_refused() {
	// A client that decodes a cursor and edits the partition inside it produces
	// one that decodes but that the store refuses. That is the same mistake as a
	// malformed cursor, so it is the same error rather than a query failure: a
	// handler mapping this to a response answers a bad request.
	api := &fakeDynamo{
		table: compositeTable(),
		err:   startKeyRefused(),
	}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{
		Partition: "c-1",
		Cursor:    validCursor(),
	}, &found)

	s.Require().ErrorIs(err, datastore.ErrInvalidCursor)
}

func (s *DatastoreTestSuite) Test_a_query_that_fails_for_another_reason_is_not_blamed_on_the_cursor() {
	// Without this, a validation failure about anything else would be reported
	// as the client's cursor being wrong.
	api := &fakeDynamo{
		table: compositeTable(),
		err: &smithy.GenericAPIError{
			Code:    "ValidationException",
			Message: "ExpressionAttributeValues contains invalid value",
		},
	}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{
		Partition: "c-1",
		Cursor:    validCursor(),
	}, &found)

	s.Require().Error(err)
	s.NotErrorIs(err, datastore.ErrInvalidCursor)
}

func (s *DatastoreTestSuite) Test_a_refusal_with_no_cursor_given_is_not_blamed_on_one() {
	api := &fakeDynamo{table: compositeTable(), err: startKeyRefused()}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{Partition: "c-1"}, &found)

	s.Require().Error(err)
	s.NotErrorIs(err, datastore.ErrInvalidCursor,
		"there was no cursor to be wrong")
}

// validCursor is a cursor this package produced, so that a case about what the
// store does with one is not about decoding it.
func validCursor() string {
	return base64.RawURLEncoding.EncodeToString([]byte(
		`{"customerId":{"t":"S","v":"c-1"},"orderId":{"t":"S","v":"o-1"}}`))
}

// startKeyRefused is what DynamoDB answers a position it will not resume a
// query from. There is no distinct error code for it, so the message is what
// identifies it.
func startKeyRefused() error {
	return &smithy.GenericAPIError{
		Code:    "ValidationException",
		Message: "The provided starting key does not match the range key predicate",
	}
}

func (s *DatastoreTestSuite) Test_reading_a_query_through_follows_every_page() {
	api := &fakeDynamo{table: compositeTable(), pages: []*dynamodb.QueryOutput{
		{
			Items: []map[string]dynamotypes.AttributeValue{
				{"orderId": &dynamotypes.AttributeValueMemberS{Value: "o-1"}},
			},
			LastEvaluatedKey: map[string]dynamotypes.AttributeValue{
				"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
				"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-1"},
			},
		},
		{
			Items: []map[string]dynamotypes.AttributeValue{
				{"orderId": &dynamotypes.AttributeValueMemberS{Value: "o-2"}},
			},
		},
	}}

	var ids []string
	for item, err := range datastore.Items[order](
		awstest.Ctx(), s.store(api), datastore.Query{Partition: "c-1"},
	) {
		s.Require().NoError(err)
		ids = append(ids, item.OrderID)
	}

	s.Equal([]string{"o-1", "o-2"}, ids)
	s.Len(api.queried, 2)
	s.NotEmpty(api.queried[1].ExclusiveStartKey)
}

func (s *DatastoreTestSuite) Test_stopping_early_stops_the_fetching() {
	api := &fakeDynamo{table: compositeTable(), pages: []*dynamodb.QueryOutput{
		{
			Items: []map[string]dynamotypes.AttributeValue{
				{"orderId": &dynamotypes.AttributeValueMemberS{Value: "o-1"}},
			},
			LastEvaluatedKey: map[string]dynamotypes.AttributeValue{
				"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
				"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-1"},
			},
		},
	}}

	for _, err := range datastore.Items[order](
		awstest.Ctx(), s.store(api), datastore.Query{Partition: "c-1"},
	) {
		s.Require().NoError(err)
		break
	}

	s.Len(api.queried, 1, "a second page should not have been asked for")
}

func (s *DatastoreTestSuite) Test_a_key_schema_that_cannot_be_read_names_the_resource() {
	api := &fakeDynamo{describe: &dynamotypes.ResourceNotFoundException{}}

	var got order
	_, err := s.store(api).Get(awstest.Ctx(), datastore.Key{Partition: "c-1"}, &got)

	s.Require().Error(err)
	s.Contains(err.Error(), `datastore "ordersTable"`)
	s.Contains(err.Error(), "key schema")
}

// namesOf and valuesOf are the attributes an expression refers to, whatever the
// builder chose to call the placeholders standing for them.
func namesOf(in *dynamodb.QueryInput) []string {
	names := make([]string, 0, len(in.ExpressionAttributeNames))
	for _, name := range in.ExpressionAttributeNames {
		names = append(names, name)
	}
	return names
}

func valuesOf(in *dynamodb.QueryInput) []dynamotypes.AttributeValue {
	values := make([]dynamotypes.AttributeValue, 0, len(in.ExpressionAttributeValues))
	for _, value := range in.ExpressionAttributeValues {
		values = append(values, value)
	}
	return values
}
