package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/stretchr/testify/suite"
)

// DynamoDB has no version of its own, so a revision is an attribute the
// provider keeps on the item. Every read has to hand one back, every write has
// to stamp a new one, and an item that predates any of this has to keep working.
type RevisionTestSuite struct {
	suite.Suite
}

func TestRevisionTestSuite(t *testing.T) {
	suite.Run(t, new(RevisionTestSuite))
}

func (s *RevisionTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

// Produces an item as DynamoDB holds it, revision attribute and all.
func storedOrder(revision string) map[string]dynamotypes.AttributeValue {
	item := map[string]dynamotypes.AttributeValue{
		"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
		"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-1"},
		"total":      &dynamotypes.AttributeValueMemberN{Value: "42"},
	}
	if revision != "" {
		item[datastore.RevisionField] = &dynamotypes.AttributeValueMemberS{Value: revision}
	}
	return item
}

func (s *RevisionTestSuite) Test_the_revision_attribute_never_reaches_the_item() {
	api := &fakeDynamo{table: compositeTable(), item: storedOrder("r-1")}

	// A map rather than a struct, since a struct would drop an attribute it has
	// no field for and hide the leak this is checking for.
	got := map[string]any{}
	_, err := s.store(api).Get(awstest.Ctx(), datastore.Key{Partition: "c-1", Sort: "o-1"}, &got)

	s.Require().NoError(err)
	s.NotContains(got, datastore.RevisionField,
		"the revision is the provider's, and an application that saw it could write it back")
	s.Contains(got, "total")
}

func (s *RevisionTestSuite) Test_a_revision_that_did_not_come_from_a_read_is_refused() {
	cases := []struct {
		name  string
		write func(datastore.Client) error
	}{
		{
			name: "a put",
			write: func(store datastore.Client) error {
				_, err := store.Put(awstest.Ctx(),
					datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
					datastore.IfUnchanged(datastore.Revision{}))
				return err
			},
		},
		{
			name: "a delete",
			write: func(store datastore.Client) error {
				return store.Delete(awstest.Ctx(),
					datastore.Key{Partition: "c-1", Sort: "o-1"},
					datastore.IfUnchanged(datastore.Revision{}))
			},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{table: compositeTable()}

			err := tc.write(s.store(api))

			s.Require().ErrorIs(err, datastore.ErrInvalidRevision)
			s.Nil(api.put, "nothing should have been written")
			s.Nil(api.deleted, "nothing should have been deleted")
		})
	}
}

func (s *RevisionTestSuite) Test_a_revision_and_a_condition_both_have_to_hold() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, order{},
		datastore.If(datastore.Eq("status", "open")),
		datastore.IfUnchanged(datastore.NewRevision("r-1")))

	s.Require().NoError(err)
	s.Contains(aws.ToString(api.put.ConditionExpression), " AND ")
	s.Contains(putNames(api), "status")
	s.Contains(putNames(api), datastore.RevisionField)
}

func (s *RevisionTestSuite) Test_a_delete_can_require_the_revision() {
	api := &fakeDynamo{table: compositeTable()}

	err := s.store(api).Delete(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		datastore.IfUnchanged(datastore.NewRevision("r-1")))

	s.Require().NoError(err)
	s.Contains(namesIn(api.deleted.ExpressionAttributeNames), datastore.RevisionField)
}

func putNames(api *fakeDynamo) []string {
	return namesIn(api.put.ExpressionAttributeNames)
}

func (s *RevisionTestSuite) Test_a_query_does_not_leak_the_revision_attribute() {
	api := &fakeDynamo{
		table: compositeTable(),
		pages: []*dynamodb.QueryOutput{{
			Items: []map[string]dynamotypes.AttributeValue{
				storedOrder("r-1"),
				storedOrder("r-2"),
			},
		}},
	}

	// A map destination, since a struct silently drops an attribute it has no
	// field for and would hide the leak.
	var found []map[string]any
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{Partition: "c-1"}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 2)
	for _, item := range found {
		s.NotContains(item, datastore.RevisionField,
			"the revision is the provider's, whichever read produced the item")
		s.Contains(item, "total")
	}
}

// revisionedOrder writes back what it read, so it takes the revision a query
// produced rather than reading each item again to get one.
type revisionedOrder struct {
	CustomerID string `dynamodbav:"customerId"`
	OrderID    string `dynamodbav:"orderId"`
	Total      int    `dynamodbav:"total"`

	rev datastore.Revision
}

func (o *revisionedOrder) SetRevision(r datastore.Revision) {
	o.rev = r
}

func (s *RevisionTestSuite) Test_a_query_hands_each_item_its_revision() {
	api := &fakeDynamo{
		table: compositeTable(),
		pages: []*dynamodb.QueryOutput{{
			Items: []map[string]dynamotypes.AttributeValue{
				storedOrder("r-1"),
				storedOrder("r-2"),
			},
		}},
	}

	var found []revisionedOrder
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{Partition: "c-1"}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 2)

	first, _ := found[0].rev.Value()
	second, _ := found[1].rev.Value()
	s.Equal("r-1", first)
	s.Equal("r-2", second, "revisions follow the order the items were read in")
}

func (s *RevisionTestSuite) Test_an_item_that_wants_no_revision_is_read_unchanged() {
	api := &fakeDynamo{
		table: compositeTable(),
		pages: []*dynamodb.QueryOutput{{
			Items: []map[string]dynamotypes.AttributeValue{storedOrder("r-1")},
		}},
	}

	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), datastore.Query{Partition: "c-1"}, &found)

	s.Require().NoError(err)
	s.Equal([]order{{CustomerID: "c-1", OrderID: "o-1", Total: 42}}, found)
}
