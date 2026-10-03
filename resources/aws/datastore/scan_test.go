package datastore_test

import (
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// A scan reads the whole table, so what it carries is a filter, a projection
// and where to resume, and nothing that belongs to a partition.
type ScanTestSuite struct {
	suite.Suite
}

func TestScanTestSuite(t *testing.T) {
	suite.Run(t, new(ScanTestSuite))
}

func (s *ScanTestSuite) scan(q datastore.Scan) *fakeDynamo {
	api := &fakeDynamo{table: compositeTable(), scans: []*dynamodb.ScanOutput{{}}}
	var found []order
	_, err := datastoreOn(s.T(), api).Scan(awstest.Ctx(), q, &found)
	s.Require().NoError(err)
	s.Require().Len(api.scanned, 1)
	return api
}

func (s *ScanTestSuite) Test_a_projection_carries_the_key_and_the_revision() {
	api := s.scan(datastore.Scan{Project: []string{"total"}})

	projected := namesIn(api.scanned[0].ExpressionAttributeNames)
	s.Contains(projected, "total")
	s.Contains(projected, "customerId")
	s.Contains(projected, "orderId")
	s.Contains(projected, datastore.RevisionField)
}

func (s *ScanTestSuite) Test_a_cursor_from_somewhere_else_is_refused() {
	api := &fakeDynamo{table: compositeTable(), scans: []*dynamodb.ScanOutput{{}}}

	var found []order
	_, err := datastoreOn(s.T(), api).Scan(
		awstest.Ctx(), datastore.Scan{Cursor: "not-a-cursor-this-store-made"}, &found)

	s.Require().Error(err)
	s.Empty(api.scanned, "nothing should have been asked of DynamoDB")
}

func (s *ScanTestSuite) Test_a_scan_does_not_leak_the_revision_attribute() {
	api := &fakeDynamo{
		table: compositeTable(),
		scans: []*dynamodb.ScanOutput{{
			Items: []map[string]dynamotypes.AttributeValue{storedOrder("r-1")},
		}},
	}

	var found []map[string]any
	_, err := datastoreOn(s.T(), api).Scan(awstest.Ctx(), datastore.Scan{}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.NotContains(found[0], datastore.RevisionField)
}

func (s *ScanTestSuite) Test_reading_a_scan_through_fetches_a_page_at_a_time() {
	api := &fakeDynamo{
		table: compositeTable(),
		scans: []*dynamodb.ScanOutput{
			{
				Items: []map[string]dynamotypes.AttributeValue{storedOrder("r-1")},
				LastEvaluatedKey: map[string]dynamotypes.AttributeValue{
					"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
					"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-1"},
				},
			},
			{Items: []map[string]dynamotypes.AttributeValue{storedOrder("r-2")}},
		},
	}

	var read int
	for _, err := range datastore.Scanned[order](awstest.Ctx(), datastoreOn(s.T(), api), datastore.Scan{}) {
		s.Require().NoError(err)
		read += 1
	}

	s.Equal(2, read)
	s.Len(api.scanned, 2, "the second page is only asked for once the first is read through")
}

func (s *ScanTestSuite) Test_stopping_early_stops_the_fetching() {
	api := &fakeDynamo{
		table: compositeTable(),
		scans: []*dynamodb.ScanOutput{{
			Items: []map[string]dynamotypes.AttributeValue{storedOrder("r-1"), storedOrder("r-2")},
			LastEvaluatedKey: map[string]dynamotypes.AttributeValue{
				"customerId": &dynamotypes.AttributeValueMemberS{Value: "c-1"},
				"orderId":    &dynamotypes.AttributeValueMemberS{Value: "o-1"},
			},
		}},
	}

	for range datastore.Scanned[order](awstest.Ctx(), datastoreOn(s.T(), api), datastore.Scan{}) {
		break
	}

	s.Len(api.scanned, 1, "a scan is the expensive read, so a break has to stop it")
}
