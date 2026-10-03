package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// A query narrows in three ways beyond the key: a filter on the item's own
// fields, a projection of what comes back, and the direction of the sort order.
type QueryOptionsTestSuite struct {
	suite.Suite
}

func TestQueryOptionsTestSuite(t *testing.T) {
	suite.Run(t, new(QueryOptionsTestSuite))
}

func (s *QueryOptionsTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

func (s *QueryOptionsTestSuite) query(q datastore.Query) *fakeDynamo {
	api := &fakeDynamo{table: compositeTable(), pages: []*dynamodb.QueryOutput{{}}}
	var found []order
	_, err := s.store(api).Query(awstest.Ctx(), q, &found)
	s.Require().NoError(err)
	s.Require().Len(api.queried, 1)
	return api
}

func (s *QueryOptionsTestSuite) Test_a_nested_filter_keeps_its_precedence() {
	api := s.query(datastore.Query{
		Partition: "c-1",
		Filter: ptr(datastore.All(
			datastore.Eq("status", "open"),
			datastore.Any(datastore.Gt("total", 100), datastore.NotExists("paidAt")),
		)),
	})

	s.Contains(aws.ToString(api.queried[0].FilterExpression), " OR ",
		"an or inside an and has to stay grouped, which is what the builder is for")
	s.Contains(aws.ToString(api.queried[0].FilterExpression), " AND ")
}

func (s *QueryOptionsTestSuite) Test_a_projection_on_an_index_carries_both_key_schemas() {
	// Resuming an index query needs the index's key and the table's, so a
	// projection that dropped either would make the next page unaskable.
	api := s.query(datastore.Query{
		Partition: "open",
		Index:     "byTotal",
		Project:   []string{"status"},
	})

	projected := namesIn(api.queried[0].ExpressionAttributeNames)
	s.Contains(projected, "total", "the index's own key")
	s.Contains(projected, "customerId", "and the table's")
	s.Contains(projected, "orderId")
}

func (s *QueryOptionsTestSuite) Test_a_projection_does_not_repeat_a_field_the_caller_listed() {
	api := s.query(datastore.Query{
		Partition: "c-1",
		Project:   []string{"customerId", "total"},
	})

	projected := namesIn(api.queried[0].ExpressionAttributeNames)
	s.Len(projected, 4, "customerId, orderId, total and the revision, each once")
}

func ptr[T any](v T) *T { return &v }
