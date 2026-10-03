package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// An update changes part of an item that is already there. DynamoDB would
// create one instead, so the difference between the two stores is what most of
// this is about.
type UpdateTestSuite struct {
	suite.Suite
}

func TestUpdateTestSuite(t *testing.T) {
	suite.Run(t, new(UpdateTestSuite))
}

func (s *UpdateTestSuite) store(api *fakeDynamo) datastore.Client {
	return datastoreOn(s.T(), api)
}

func (s *UpdateTestSuite) update(updates ...datastore.Update) *fakeDynamo {
	api := &fakeDynamo{table: compositeTable()}
	_, err := s.store(api).Update(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, updates)
	s.Require().NoError(err)
	s.Require().NotNil(api.updated)
	return api
}

func (s *UpdateTestSuite) Test_a_nested_path_addresses_a_field_inside_an_object() {
	api := s.update(datastore.Set("profile.theme", "dark"))

	named := namesIn(api.updated.ExpressionAttributeNames)
	s.Contains(named, "profile")
	s.Contains(named, "theme")
}

func (s *UpdateTestSuite) Test_a_condition_and_a_revision_join_the_existence_check() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Update(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"},
		[]datastore.Update{datastore.Set("status", "refunded")},
		datastore.If(datastore.Eq("status", "paid")),
		datastore.IfUnchanged(datastore.NewRevision("r-1")))

	s.Require().NoError(err)
	condition := aws.ToString(api.updated.ConditionExpression)
	s.Contains(condition, "attribute_exists")
	s.Contains(condition, " AND ")
	s.Contains(namesIn(api.updated.ExpressionAttributeNames), "status")
}

func (s *UpdateTestSuite) Test_an_update_that_asks_for_nothing_is_refused() {
	api := &fakeDynamo{table: compositeTable()}

	_, err := s.store(api).Update(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, nil)

	s.Require().ErrorIs(err, datastore.ErrInvalidUpdate)
	s.Nil(api.updated, "nothing should have been sent")
}

func (s *UpdateTestSuite) Test_more_changes_than_the_portable_ceiling_are_refused_locally() {
	api := &fakeDynamo{table: compositeTable()}

	updates := make([]datastore.Update, datastore.MaxUpdates+1)
	for i := range updates {
		updates[i] = datastore.Increment("attempts", 1)
	}

	_, err := s.store(api).Update(awstest.Ctx(),
		datastore.Key{Partition: "c-1", Sort: "o-1"}, updates)

	s.Require().ErrorIs(err, datastore.ErrTooManyOperations)
	s.Nil(api.updated,
		"DynamoDB would have taken it, which is exactly why it is refused here")
}

func (s *UpdateTestSuite) Test_a_path_that_names_nothing_is_refused() {
	cases := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "a leading dot", path: ".status"},
		{name: "a doubled dot", path: "profile..theme"},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{table: compositeTable()}

			_, err := s.store(api).Update(awstest.Ctx(),
				datastore.Key{Partition: "c-1", Sort: "o-1"},
				[]datastore.Update{datastore.Set(tc.path, "x")})

			s.Require().ErrorIs(err, datastore.ErrInvalidUpdate)
			s.Nil(api.updated)
		})
	}
}

func (s *UpdateTestSuite) Test_a_refusal_says_whether_the_item_was_there() {
	cases := []struct {
		name string
		item map[string]dynamotypes.AttributeValue
		want error
	}{
		{
			name: "no item came back, so there was none",
			item: nil,
			want: datastore.ErrNotFound,
		},
		{
			name: "an item came back, so a condition is what failed",
			item: storedOrder("r-1"),
			want: datastore.ErrConditionFailed,
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			api := &fakeDynamo{
				table: compositeTable(),
				err:   &dynamotypes.ConditionalCheckFailedException{Item: tc.item},
			}

			_, err := s.store(api).Update(awstest.Ctx(),
				datastore.Key{Partition: "c-1", Sort: "o-1"},
				[]datastore.Update{datastore.Set("status", "shipped")})

			s.Require().ErrorIs(err, tc.want)
		})
	}
}
