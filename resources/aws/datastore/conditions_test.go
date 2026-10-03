package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsdatastore "github.com/newstack-cloud/celerity-go-sdk/resources/aws/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// The two things a store cannot be used safely without.
//
// A sort condition is what lets a query read part of a partition rather than
// all of it. A write condition is what makes a
// read-then-write safe, since two invocations of a handler run at the same time
// and neither can otherwise tell that the item changed underneath it.
type ConditionsTestSuite struct {
	suite.Suite
}

func TestConditionsTestSuite(t *testing.T) {
	suite.Run(t, new(ConditionsTestSuite))
}

func (s *ConditionsTestSuite) store(api *fakeDynamo) datastore.Client {
	store, err := awsdatastore.Stores(api)(
		awstest.SimpleRef(resources.KindDatastore, "ordersTable", "orders-prod"),
	)
	s.Require().NoError(err)
	return store
}

func (s *ConditionsTestSuite) Test_a_sort_value_is_sent_as_the_type_the_table_declared() {
	// A sort key declared as a number and compared against a string is refused,
	// and a handler's sort value is a string because not every store has types.
	api := &fakeDynamo{table: numericSortTable(), pages: []*dynamodb.QueryOutput{{}}}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{
		Partition: "c-1",
		Sort:      datastore.SortGreaterOrEqual("100"),
	}, &found)

	s.Require().NoError(err)
	s.Contains(valuesOf(api.queried[0]), dynamotypes.AttributeValue(
		&dynamotypes.AttributeValueMemberN{Value: "100"}))
}

func (s *ConditionsTestSuite) Test_narrowing_a_key_that_has_no_sort_attribute_is_refused() {
	// Sending it would be a condition on an attribute the table does not key
	// on, which DynamoDB refuses in a way that says nothing about the handler.
	api := &fakeDynamo{table: partitionOnlyTable()}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{
		Partition: "c-1",
		Sort:      datastore.SortStartsWith("o-"),
	}, &found)

	s.Require().Error(err)
	s.Contains(err.Error(), "alone")
	s.Empty(api.queried)
}

func (s *ConditionsTestSuite) Test_a_sort_condition_built_by_hand_is_refused() {
	// The zero operator, which is what a SortCondition built as a struct
	// literal rather than by one of the Sort functions has.
	api := &fakeDynamo{table: compositeTable()}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{
		Partition: "c-1",
		Sort:      &datastore.SortCondition{Value: "o-1"},
	}, &found)

	s.Require().ErrorIs(err, datastore.ErrInvalidCondition)
	s.Empty(api.queried)
}

func (s *ConditionsTestSuite) Test_a_group_of_no_conditions_is_refused() {
	// It would read as a write with no condition, which is the opposite of
	// what asking for one means.
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
		datastore.If(datastore.All()))

	s.Require().Error(err)
	s.Contains(err.Error(), "no condition at all")
	s.Nil(api.put)
}

func (s *ConditionsTestSuite) Test_every_operator_becomes_an_expression() {
	// The whole portable set, so an operator that was added and never wired up
	// is caught here rather than by whoever first used it.
	cases := map[string]datastore.Condition{
		"=":                datastore.Eq("a", 1),
		"<>":               datastore.Ne("a", 1),
		"<":                datastore.Lt("a", 1),
		"<=":               datastore.Le("a", 1),
		">":                datastore.Gt("a", 1),
		">=":               datastore.Ge("a", 1),
		"BETWEEN":          datastore.Between("a", 1, 2),
		"begins_with":      datastore.StartsWith("a", "x"),
		"contains":         datastore.Contains("a", "x"),
		"attribute_exists": datastore.Exists("a"),
	}

	for expresses, condition := range cases {
		s.Run(expresses, func() {
			api := &fakeDynamo{table: compositeTable()}

			_, err := s.store(api).Put(awstest.Ctx(),
				datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
				datastore.If(condition))

			s.Require().NoError(err)
			s.Contains(aws.ToString(api.put.ConditionExpression), expresses)
		})
	}
}

func (s *ConditionsTestSuite) Test_a_condition_built_by_hand_is_refused() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
		datastore.If(datastore.Condition{Name: "status", Value: "open"}))

	s.Require().ErrorIs(err, datastore.ErrInvalidCondition)
	s.Nil(api.put)
}

func (s *ConditionsTestSuite) Test_a_refused_write_is_told_apart_from_a_failure() {
	// A handler retrying should not have to know which store it is talking to,
	// or match on an AWS error code to find out it lost a race.
	cases := []struct {
		name  string
		write func(datastore.Client) error
	}{
		{
			name: "a put",
			write: func(store datastore.Client) error {
				_, err := store.Put(awstest.Ctx(),
					datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
					datastore.If(datastore.Eq("version", 3)))
				return err
			},
		},
		{
			name: "a delete",
			write: func(store datastore.Client) error {
				return store.Delete(awstest.Ctx(),
					datastore.Key{Partition: "c-1", Sort: "o-1"},
					datastore.If(datastore.Eq("version", 3)))
			},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{
				table: compositeTable(),
				err:   &dynamotypes.ConditionalCheckFailedException{},
			}

			err := tc.write(s.store(api))

			s.Require().Error(err)
			s.ErrorIs(err, datastore.ErrConditionFailed)
			s.Contains(err.Error(), `datastore "ordersTable"`)
		})
	}
}

func (s *ConditionsTestSuite) Test_a_failure_that_is_not_a_refusal_stays_a_failure() {
	// Reporting a throttle as a lost race would have a handler retry the write
	// forever on the assumption someone else got there first.
	api := &fakeDynamo{
		table: compositeTable(),
		err:   &dynamotypes.ProvisionedThroughputExceededException{},
	}

	_, err := s.store(api).Put(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
		datastore.If(datastore.Eq("version", 3)))

	s.Require().Error(err)
	s.NotErrorIs(err, datastore.ErrConditionFailed)
}

// numericSortTable is keyed on a string partition and a numeric sort attribute.
func numericSortTable() *dynamotypes.TableDescription {
	return &dynamotypes.TableDescription{
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("customerId"),
				AttributeType: dynamotypes.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String("orderId"),
				AttributeType: dynamotypes.ScalarAttributeTypeN,
			},
		},
		KeySchema: []dynamotypes.KeySchemaElement{
			{
				AttributeName: aws.String("customerId"),
				KeyType:       dynamotypes.KeyTypeHash,
			},
			{
				AttributeName: aws.String("orderId"),
				KeyType:       dynamotypes.KeyTypeRange,
			},
		},
	}
}

func namesIn(names map[string]string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name)
	}

	return out
}
