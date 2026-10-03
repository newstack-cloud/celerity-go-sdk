package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// An item type is the application's, not DynamoDB's: the same struct is meant to
// work against three stores and is what codegen emits from a schema naming none
// of them. So the tag it carries has to be json rather than a provider's own.
type MarshalTestSuite struct {
	suite.Suite
}

func TestMarshalTestSuite(t *testing.T) {
	suite.Run(t, new(MarshalTestSuite))
}

// portableOrder carries no provider's tag, which is what a generated item type
// looks like.
type portableOrder struct {
	CustomerID string `json:"customerId"`
	OrderID    string `json:"orderId"`
	Total      int    `json:"total"`
	PaidAt     string `json:"paidAt,omitempty"`
}

func (s *MarshalTestSuite) Test_a_json_tagged_item_is_written_under_the_schema_field_names() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := datastoreOn(s.T(), api).Put(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		portableOrder{Total: 42})

	s.Require().NoError(err)
	s.Contains(api.put.Item, "total",
		"a Go field name of Total would be unreadable to every other SDK")
	s.NotContains(api.put.Item, "Total")
	s.Equal(&dynamotypes.AttributeValueMemberN{Value: "42"}, api.put.Item["total"])
}

func (s *MarshalTestSuite) Test_omitempty_is_honoured_so_an_absent_field_stays_absent() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := datastoreOn(s.T(), api).Put(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		portableOrder{Total: 42})

	s.Require().NoError(err)
	s.NotContains(api.put.Item, "paidAt")
}

func (s *MarshalTestSuite) Test_every_read_and_write_path_uses_the_same_tag() {
	cases := []struct {
		name  string
		write func(datastore.Client) error
		item  func(*fakeDynamo) map[string]dynamotypes.AttributeValue
	}{
		{
			name: "a put",
			write: func(store datastore.Client) error {
				_, err := store.Put(awstest.Ctx(),
					datastore.Key{Partition: "c-1", Sort: "o-1"}, portableOrder{Total: 1})
				return err
			},
			item: func(api *fakeDynamo) map[string]dynamotypes.AttributeValue {
				return api.put.Item
			},
		},
		{
			name: "a put in a batch",
			write: func(store datastore.Client) error {
				_, err := store.BatchWrite(awstest.Ctx(), []datastore.BatchOp{
					datastore.PutOp(
						datastore.Key{Partition: "c-1", Sort: "o-1"}, portableOrder{Total: 1}),
				})
				return err
			},
			item: func(api *fakeDynamo) map[string]dynamotypes.AttributeValue {
				return api.batchPut[0].RequestItems["orders-prod"][0].PutRequest.Item
			},
		},
		{
			name: "a put in an atomic write",
			write: func(store datastore.Client) error {
				return store.Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
					datastore.AtomicPut(
						datastore.Key{Partition: "c-1", Sort: "o-1"}, portableOrder{Total: 1}),
				})
			},
			item: func(api *fakeDynamo) map[string]dynamotypes.AttributeValue {
				return api.transacted.TransactItems[0].Put.Item
			},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{table: compositeTable()}

			s.Require().NoError(tc.write(datastoreOn(s.T(), api)))

			s.Contains(tc.item(api), "total")
			s.NotContains(tc.item(api), "Total")
		})
	}
}

// A put replaces what is under a key rather than moving an item between keys.
//
// The key is written over the item, so an item carrying a key field that
// disagrees would have the field silently discarded and be told the write
// succeeded. That is the one failure nothing would notice, so it is refused.
type KeyConflictTestSuite struct {
	suite.Suite
}

func TestKeyConflictTestSuite(t *testing.T) {
	suite.Run(t, new(KeyConflictTestSuite))
}

func (s *KeyConflictTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

func (s *KeyConflictTestSuite) Test_an_item_carrying_a_different_key_is_refused() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(
		awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		order{CustomerID: "c-2", OrderID: "o-1", Total: 42},
	)

	s.Require().Error(err)
	s.Contains(err.Error(), `"customerId"`, "the error names the attribute that disagreed")
	s.Contains(err.Error(), "c-2")
	s.Contains(err.Error(), "c-1")
	s.Contains(err.Error(), "rather than moving an item",
		"and says what a put will not do")
	s.Nil(api.put, "nothing should have been written")
}

func (s *KeyConflictTestSuite) Test_a_sort_value_that_disagrees_is_refused() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(
		awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		order{CustomerID: "c-1", OrderID: "o-9"},
	)

	s.Require().Error(err)
	s.Contains(err.Error(), `"orderId"`)
	s.Nil(api.put)
}

func (s *KeyConflictTestSuite) Test_an_item_that_agrees_is_written() {
	// Which is what read-modify-write produces: a read fills the key fields
	// from the stored item, so they already say what the key says.
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(
		awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		order{CustomerID: "c-1", OrderID: "o-1", Total: 42},
	)

	s.Require().NoError(err)
	s.Require().NotNil(api.put)
	s.Equal("c-1", stringAttr(api.put.Item, "customerId"))
}

func (s *KeyConflictTestSuite) Test_an_item_that_carries_no_key_fields_is_written() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(
		awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		struct {
			Total int `json:"total"`
		}{Total: 42},
	)

	s.Require().NoError(err)
	s.Require().NotNil(api.put)
	s.Equal("c-1", stringAttr(api.put.Item, "customerId"),
		"the key is still what addresses the item")
}

func (s *KeyConflictTestSuite) Test_key_fields_a_struct_leaves_empty_are_filled_in() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Put(
		awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		order{Total: 42},
	)

	s.Require().NoError(err)
	s.Require().NotNil(api.put)
	s.Equal("c-1", stringAttr(api.put.Item, "customerId"))
	s.Equal("o-1", stringAttr(api.put.Item, "orderId"))
}

func (s *KeyConflictTestSuite) Test_a_batch_put_is_refused_the_same_way() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).BatchWrite(awstest.Ctx(), []datastore.BatchOp{
		datastore.PutOp(datastore.Key{Partition: "c-1", Sort: "o-1"},
			order{CustomerID: "c-2", OrderID: "o-1"}),
	})

	s.Require().Error(err)
	s.Contains(err.Error(), `"customerId"`)
	s.Empty(api.batchPut, "nothing should have been sent")
}

func (s *KeyConflictTestSuite) Test_an_atomic_put_is_refused_the_same_way() {
	api := &fakeDynamo{table: compositeTable()}

	err := s.store(api).Atomically(awstest.Ctx(), "c-1", []datastore.AtomicOp{
		datastore.AtomicPut(datastore.Key{Partition: "c-1", Sort: "o-1"},
			order{CustomerID: "c-1", OrderID: "o-9"}),
	})

	s.Require().Error(err)
	s.Contains(err.Error(), `"orderId"`)
	s.Nil(api.transacted, "nothing should have been sent")
}

// Reads a string attribute of a request, for a case about what was
// sent rather than about how it was encoded.
func stringAttr(item map[string]dynamotypes.AttributeValue, name string) string {
	value, ok := item[name].(*dynamotypes.AttributeValueMemberS)
	if !ok {
		return ""
	}
	return value.Value
}
