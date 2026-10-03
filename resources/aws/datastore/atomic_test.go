package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// An atomic write is the narrowest of the stores rather than the widest, so
// most of this is about what is refused here that DynamoDB would have taken.
type AtomicTestSuite struct {
	suite.Suite
}

func TestAtomicTestSuite(t *testing.T) {
	suite.Run(t, new(AtomicTestSuite))
}

func (s *AtomicTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

func at(sort string) datastore.Key {
	return datastore.Key{Partition: "c-1", Sort: sort}
}

func (s *AtomicTestSuite) Test_a_write_of_nothing_asks_nothing() {
	api := &fakeDynamo{table: compositeTable()}

	err := s.store(api).Atomically(awstest.Ctx(), "c-1", nil)

	s.Require().NoError(err)
	s.Nil(api.transacted)
}

func (s *AtomicTestSuite) Test_an_operation_outside_the_partition_is_refused_locally() {
	api := &fakeDynamo{table: compositeTable()}

	// Atomic operations with this portable API allows for related items
	// that share a partition key (e.g. customerId) but are differentiated by sort key
	// to be operated on as a single atomic action.
	err := s.store(api).Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
		datastore.AtomicPut(at("o-1"), order{Total: 1}),
		datastore.AtomicPut(datastore.Key{Partition: "c-2", Sort: "o-1"}, order{Total: 2}),
	})

	s.Require().ErrorIs(err, datastore.ErrWrongPartition)
	s.Nil(api.transacted,
		"DynamoDB would have applied it across partitions, and Cosmos DB could not have")
}

func (s *AtomicTestSuite) Test_more_operations_than_the_ceiling_are_refused_locally() {
	api := &fakeDynamo{table: compositeTable()}

	ops := make([]datastore.AtomicOp, datastore.MaxAtomicOps+1)
	for i := range ops {
		ops[i] = datastore.AtomicDelete(at("o-1"))
	}

	err := s.store(api).Atomically(awstest.Ctx(), "c-1", ops)

	s.Require().ErrorIs(err, datastore.ErrTooManyOperations)
	s.Nil(api.transacted)
}

func (s *AtomicTestSuite) Test_each_operation_carries_its_own_preconditions() {
	api := &fakeDynamo{table: compositeTable()}

	err := s.store(api).Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
		datastore.AtomicPut(
			at("o-1"), order{Total: 1},
			datastore.IfUnchanged(datastore.NewRevision("r-1")),
		),
		datastore.AtomicDelete(
			at("o-2"), datastore.If(datastore.Eq("status", "open")),
		),
	})

	s.Require().NoError(err)
	items := api.transacted.TransactItems
	s.Contains(namesIn(items[0].Put.ExpressionAttributeNames), datastore.RevisionField)
	s.Contains(namesIn(items[1].Delete.ExpressionAttributeNames), "status")
}

func (s *AtomicTestSuite) Test_a_put_and_an_update_both_stamp_a_revision() {
	api := &fakeDynamo{table: compositeTable()}

	err := s.store(api).Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
		datastore.AtomicPut(at("o-1"), order{Total: 1}),
		datastore.AtomicUpdate(
			at("o-2"), []datastore.Update{datastore.Set("status", "paid")},
		),
	})

	s.Require().NoError(err)
	items := api.transacted.TransactItems
	s.Contains(items[0].Put.Item, datastore.RevisionField)
	s.Contains(namesIn(items[1].Update.ExpressionAttributeNames), datastore.RevisionField)
}

func (s *AtomicTestSuite) Test_an_update_in_a_write_obeys_the_update_ceiling() {
	api := &fakeDynamo{table: compositeTable()}

	updates := make([]datastore.Update, datastore.MaxUpdates+1)
	for i := range updates {
		updates[i] = datastore.Increment("total", 1)
	}

	err := s.store(api).Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
		datastore.AtomicUpdate(at("o-1"), updates),
	})

	s.Require().ErrorIs(err, datastore.ErrTooManyOperations)
	s.Nil(api.transacted)
}

func (s *AtomicTestSuite) Test_a_refused_precondition_cancels_the_whole_write() {
	cases := []struct {
		name    string
		reasons []dynamotypes.CancellationReason
		want    error
	}{
		{
			name: "a precondition did not hold",
			reasons: []dynamotypes.CancellationReason{
				{Code: aws.String("None")},
				{Code: aws.String("ConditionalCheckFailed")},
			},
			want: datastore.ErrConditionFailed,
		},
		{
			// A conflict or a capacity problem is answered by retrying the
			// whole write, not by reading and deciding again.
			name: "something else cancelled it",
			reasons: []dynamotypes.CancellationReason{
				{Code: aws.String("TransactionConflict")},
			},
			want: nil,
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{
				table: compositeTable(),
				err: &dynamotypes.TransactionCanceledException{
					CancellationReasons: tc.reasons,
				},
			}

			err := s.store(api).Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
				datastore.AtomicDelete(at("o-1")),
			})

			s.Require().Error(err)
			if tc.want != nil {
				s.ErrorIs(err, tc.want)
			} else {
				s.NotErrorIs(err, datastore.ErrConditionFailed)
			}
		})
	}
}
