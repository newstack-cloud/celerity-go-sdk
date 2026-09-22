//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// Consumer and schedule handlers, dispatched by the real runtime.
type RuntimeConsumerTestSuite struct {
	suite.Suite
}

func TestRuntimeConsumerTestSuite(t *testing.T) {
	suite.Run(t, new(RuntimeConsumerTestSuite))
}

type processedReport struct {
	Orders         []string       `json:"orders"`
	Schedules      []string       `json:"schedules"`
	PoisonAttempts map[string]int `json:"poisonAttempts"`
}

// The streams the runtime's local consumer reads, named by the source ids the
// blueprint gave them.
const (
	ordersStream = "celerity:queue:ordersQueue"
	reportStream = "celerity:schedules:nightlyReport"
)

// Asks the application what its consumer and schedule handlers have
// seen so far.
func (s *RuntimeConsumerTestSuite) processed() processedReport {
	res, err := http.Get(runtimeURL() + "/processed")
	s.Require().NoError(err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	s.Require().NoError(err)
	s.Require().Equal(http.StatusOK, res.StatusCode, "answered %s", body)

	var report processedReport
	s.Require().NoError(json.Unmarshal(body, &report))
	return report
}

// Waits for an order to reach the consumer, since a batch is
// delivered by the runtime polling rather than by the call that published it.
func (s *RuntimeConsumerTestSuite) waitForOrder(orderID string) {
	s.Require().Eventually(func() bool {
		for _, seen := range s.processed().Orders {
			if seen == orderID {
				return true
			}
		}
		return false
	}, 30*time.Second, 250*time.Millisecond, "order %s never reached the consumer", orderID)
}

func (s *RuntimeConsumerTestSuite) Test_a_message_reaches_a_consumer_handler() {
	orderID := fmt.Sprintf("order-%d", time.Now().UnixNano())

	_, err := publish(ordersStream, fmt.Sprintf(`{"orderId":%q}`, orderID))
	s.Require().NoError(err)

	// Reaching the handler at all means the tag the runtime built from the
	// blueprint's source id is the one the SDK declared at the handshake.
	s.waitForOrder(orderID)
}

func (s *RuntimeConsumerTestSuite) Test_a_batch_of_messages_all_reach_the_handler() {
	base := time.Now().UnixNano()
	ids := []string{
		fmt.Sprintf("order-%d-a", base),
		fmt.Sprintf("order-%d-b", base),
		fmt.Sprintf("order-%d-c", base),
	}

	for _, id := range ids {
		_, err := publish(ordersStream, fmt.Sprintf(`{"orderId":%q}`, id))
		s.Require().NoError(err)
	}

	for _, id := range ids {
		s.waitForOrder(id)
	}
}

func (s *RuntimeConsumerTestSuite) Test_one_failing_record_does_not_take_the_batch_with_it() {
	base := time.Now().UnixNano()
	good := fmt.Sprintf("order-%d-good", base)
	poison := fmt.Sprintf("order-%d-poison", base)

	_, err := publish(ordersStream, fmt.Sprintf(`{"orderId":%q}`, good))
	s.Require().NoError(err)
	_, err = publish(ordersStream, fmt.Sprintf(`{"orderId":%q,"poison":true}`, poison))
	s.Require().NoError(err)

	// The record the handler could handle is handled, and is not delivered
	// again because the handler did not name it.
	s.waitForOrder(good)

	// The one it named is left on its source and comes back. Both halves
	// matter, without this, the test would pass just as well if a named record
	// were silently dropped, which is the failure mode that loses messages.
	s.Require().Eventually(func() bool {
		return s.processed().PoisonAttempts[poison] >= 2
	}, 60*time.Second, 500*time.Millisecond,
		"the failed record was not delivered again, so naming it settled it")

	// Make sure the good record was handled once.
	count := 0
	for _, seen := range s.processed().Orders {
		if seen == good {
			count++
		}
	}
	s.Equal(1, count, "a partial failure redrove the whole batch")
}

func (s *RuntimeConsumerTestSuite) Test_a_schedule_reaches_its_handler() {
	// Locally the runtime does not run a clock of its own, so a trigger is something
	// published to the schedule's stream. What is being checked is the
	// dispatch, not the timing.
	_, err := publish(reportStream, `{"window":"24h"}`)
	s.Require().NoError(err)

	s.Require().Eventually(func() bool {
		for _, id := range s.processed().Schedules {
			// The source id the blueprint gave the schedule, which the handler
			// reads off the trigger.
			if id == "nightlyReport" {
				return true
			}
		}
		return false
	}, 30*time.Second, 250*time.Millisecond, "the schedule never reached its handler")
}
