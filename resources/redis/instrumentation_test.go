package redis_test

import (
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"

	celerityredis "github.com/newstack-cloud/celerity-go-sdk/resources/redis"
)

// The seam a module traces the commands through, which is what lets a cache be
// instrumented without this module depending on a tracing library.
type InstrumentationTestSuite struct {
	suite.Suite
}

func TestInstrumentationTestSuite(t *testing.T) {
	suite.Run(t, new(InstrumentationTestSuite))
}

func (s *InstrumentationTestSuite) Test_hooks_run_in_the_order_they_were_registered() {
	// More than one is allowed, since tracing and metrics are commonly
	// separate hooks and a deployment may want both.
	var ran []string
	s.T().Cleanup(celerityredis.ClearInstrumentation)
	celerityredis.Instrument(func(goredis.UniversalClient) error {
		ran = append(ran, "first")
		return nil
	})
	celerityredis.Instrument(func(goredis.UniversalClient) error {
		ran = append(ran, "second")
		return nil
	})

	err := celerityredis.ApplyInstrumentation(nil)

	s.Require().NoError(err)
	s.Equal([]string{"first", "second"}, ran)
}

func (s *InstrumentationTestSuite) Test_a_hook_that_fails_stops_the_rest_and_is_reported() {
	// Rather than being ignored: an application that linked the module and is
	// not being traced has a problem worth hearing about.
	var reached bool
	failed := errors.New("no exporter configured")
	s.T().Cleanup(celerityredis.ClearInstrumentation)
	celerityredis.Instrument(func(goredis.UniversalClient) error {
		return failed
	})
	celerityredis.Instrument(func(goredis.UniversalClient) error {
		reached = true
		return nil
	})

	err := celerityredis.ApplyInstrumentation(nil)

	s.ErrorIs(err, failed)
	s.False(reached, "the one after it did not run")
}

func (s *InstrumentationTestSuite) Test_no_hooks_is_not_a_failure() {
	s.T().Cleanup(celerityredis.ClearInstrumentation)
	celerityredis.ClearInstrumentation()

	s.NoError(celerityredis.ApplyInstrumentation(nil))
}
