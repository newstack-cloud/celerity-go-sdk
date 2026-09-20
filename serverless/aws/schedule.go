package aws

import (
	"encoding/json"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// ToScheduleTrigger maps an EventBridge event into a schedule trigger.
//
// The schedule expression is not in the event. The rule holds it, and a
// function is not told the rule that invoked it. It is left empty rather than
// guessed at, so a handler that needs the expression reads it from its own
// configuration.
func (m Mapper) ToScheduleTrigger(payload []byte, tag string) (*handler.ScheduleTrigger, error) {
	event, err := decode[events.CloudWatchEvent](payload, "EventBridge")
	if err != nil {
		return nil, err
	}

	vendor, err := json.Marshal(map[string]any{
		"source":     event.Source,
		"detailType": event.DetailType,
		"account":    event.AccountID,
		"region":     event.Region,
		"resources":  event.Resources,
	})
	if err != nil {
		return nil, err
	}

	return &handler.ScheduleTrigger{
		ScheduleID: scheduleIDOf(tag, event.Resources),
		MessageID:  event.ID,
		Input:      event.Detail,
		Vendor:     vendor,
	}, nil
}

// Names the schedule that fired, preferring the id the blueprint
// gave it, which the handler tag carries, over the rule's ARN.
func scheduleIDOf(tag string, resources []string) string {
	if rest, found := strings.CutPrefix(tag, "source::"); found {
		if sourceID, _, ok := strings.Cut(rest, "::"); ok {
			return sourceID
		}
	}

	if len(resources) > 0 {
		return arnResourceSegment(resources[0], "rule/")
	}

	return ""
}
