package aws_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"

	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

type ScheduleTestSuite struct {
	suite.Suite
	mapper awsadapter.Mapper
}

func TestScheduleTestSuite(t *testing.T) {
	suite.Run(t, new(ScheduleTestSuite))
}

func (s *ScheduleTestSuite) Test_an_eventbridge_event_becomes_a_trigger() {
	trigger, err := s.mapper.ToScheduleTrigger(
		fixture(&s.Suite, "eventbridge.json"), "source::nightly::buildReport")

	s.Require().NoError(err)
	s.Equal("nightly", trigger.ScheduleID)
	s.Equal("eb-event-1", trigger.MessageID)
	s.JSONEq(`{"window":"24h"}`, string(trigger.Input))
	// The rule holds the expression and a function is not told which rule
	// invoked it, so this is left empty rather than guessed at.
	s.Empty(trigger.Schedule)
}

func (s *ScheduleTestSuite) Test_the_rule_names_the_schedule_where_the_tag_does_not() {
	trigger, err := s.mapper.ToScheduleTrigger(fixture(&s.Suite, "eventbridge.json"), "")

	s.Require().NoError(err)
	s.Equal("nightly-report", trigger.ScheduleID)
}

func (s *ScheduleTestSuite) Test_the_vendor_detail_carries_what_fired_it() {
	trigger, err := s.mapper.ToScheduleTrigger(fixture(&s.Suite, "eventbridge.json"), "")

	s.Require().NoError(err)

	var vendor map[string]any
	s.Require().NoError(json.Unmarshal(trigger.Vendor, &vendor))
	s.Equal("aws.events", vendor["source"])
	s.Equal("Scheduled Event", vendor["detailType"])
	s.Equal("eu-west-2", vendor["region"])
}

func (s *ScheduleTestSuite) Test_an_event_of_the_wrong_shape_is_refused() {
	_, err := s.mapper.ToScheduleTrigger([]byte(`"a string"`), "")

	s.Require().Error(err)
	s.Contains(err.Error(), "EventBridge")
}
