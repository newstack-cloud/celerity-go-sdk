package datastore_test

import (
	"fmt"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// A batch is split to the service's limits, and what the service could not get
// to comes back to the caller rather than being retried here.
type BatchTestSuite struct {
	suite.Suite
}

func TestBatchTestSuite(t *testing.T) {
	suite.Run(t, new(BatchTestSuite))
}

func (s *BatchTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

// n keys in one partition, each distinct.
//
// Distinct matters rather than being tidy: a batch is addressed by key, and
// DynamoDB refuses a request that names the same key twice, so a fixture that
// repeated them would describe a batch no service would accept.
func keys(n int) []datastore.Key {
	built := make([]datastore.Key, n)
	for i := range built {
		built[i] = datastore.Key{Partition: "c-1", Sort: fmt.Sprintf("k-%03d", i)}
	}
	return built
}

func (s *BatchTestSuite) Test_a_batch_get_of_nothing_asks_nothing() {
	api := &fakeDynamo{table: compositeTable()}

	var found []order
	missed, err := s.store(api).BatchGet(awstest.Ctx(), nil, &found)

	s.Require().NoError(err)
	s.Empty(missed)
	s.Empty(api.batchGot, "an empty batch is not a request")
}

func (s *BatchTestSuite) Test_a_batch_get_is_split_to_a_hundred_keys_a_request() {
	api := &fakeDynamo{table: compositeTable()}

	var found []order
	_, err := s.store(api).BatchGet(awstest.Ctx(), keys(250), &found)

	s.Require().NoError(err)
	s.Len(api.batchGot, 3, "250 keys is three requests, not one refusal")
	s.Len(api.batchGot[0].RequestItems["orders-prod"].Keys, 100)
	s.Len(api.batchGot[2].RequestItems["orders-prod"].Keys, 50)
}

func (s *BatchTestSuite) Test_a_batch_get_returns_the_keys_it_could_not_fetch() {
	api := &fakeDynamo{
		table: compositeTable(),
		gets: []*dynamodb.BatchGetItemOutput{{
			Responses: map[string][]map[string]dynamotypes.AttributeValue{
				"orders-prod": {storedOrder("r-1")},
			},
			UnprocessedKeys: map[string]dynamotypes.KeysAndAttributes{
				"orders-prod": {Keys: []map[string]dynamotypes.AttributeValue{{
					"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-9"},
					"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-9"},
				}}},
			},
		}},
	}

	var found []order
	missed, err := s.store(api).BatchGet(
		awstest.Ctx(),
		[]datastore.Key{{Partition: "c-1", Sort: "o-1"}},
		&found,
	)

	s.Require().NoError(err)
	s.Len(found, 1)
	s.Equal([]datastore.Key{{Partition: "c-9", Sort: "o-9"}}, missed,
		"a throttled key comes back as the caller's own key, to retry")
}

func (s *BatchTestSuite) Test_a_batch_get_does_not_leak_the_revision_attribute() {
	api := &fakeDynamo{
		table: compositeTable(),
		gets: []*dynamodb.BatchGetItemOutput{{
			Responses: map[string][]map[string]dynamotypes.AttributeValue{
				"orders-prod": {storedOrder("r-1")},
			},
		}},
	}

	var found []map[string]any
	_, err := s.store(api).BatchGet(awstest.Ctx(),
		[]datastore.Key{{Partition: "c-1", Sort: "o-1"}}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.NotContains(found[0], datastore.RevisionField)
}

func (s *BatchTestSuite) Test_a_batch_write_is_split_to_twenty_five_operations_a_request() {
	api := &fakeDynamo{table: compositeTable()}

	written := keys(60)
	ops := make([]datastore.BatchOp, len(written))
	for i, key := range written {
		ops[i] = datastore.PutOp(key, order{Total: i})
	}

	missed, err := s.store(api).BatchWrite(awstest.Ctx(), ops)

	s.Require().NoError(err)
	s.Empty(missed)
	s.Len(api.batchPut, 3)
	s.Len(api.batchPut[0].RequestItems["orders-prod"], 25)
	s.Len(api.batchPut[1].RequestItems["orders-prod"], 25)
	s.Len(api.batchPut[2].RequestItems["orders-prod"], 10)
}

func (s *BatchTestSuite) Test_a_batch_write_returns_the_operations_it_could_not_apply() {
	api := &fakeDynamo{
		table: compositeTable(),
		writes: []*dynamodb.BatchWriteItemOutput{{
			UnprocessedItems: map[string][]dynamotypes.WriteRequest{
				"orders-prod": {{
					DeleteRequest: &dynamotypes.DeleteRequest{
						Key: map[string]dynamotypes.AttributeValue{
							"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
							"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-2"},
						},
					},
				}},
			},
		}},
	}

	put := datastore.PutOp(datastore.Key{Partition: "c-1", Sort: "o-1"}, order{Total: 1})
	remove := datastore.DeleteOp(datastore.Key{Partition: "c-1", Sort: "o-2"})

	missed, err := s.store(api).BatchWrite(awstest.Ctx(), []datastore.BatchOp{put, remove})

	s.Require().NoError(err)
	s.Equal([]datastore.BatchOp{remove}, missed,
		"what comes back is the caller's own operation, so a retry is of their values")
}
