package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// A batch of spans is held so that exporting is not a request per span, so
// something has to ask for it before the process goes. Which is a seam rather
// than a call into an exporter, because core mustn't depend on any particular
// tracing library.
type FlushTestSuite struct {
	suite.Suite
}

func TestFlushTestSuite(t *testing.T) {
	suite.Run(t, new(FlushTestSuite))
}

func (s *FlushTestSuite) TearDownTest() {
	telemetry.SetFlusher(nil)
}

func (s *FlushTestSuite) Test_flushing_without_one_installed_is_not_a_failure() {
	// An application that exports nothing has nothing to hand over, which is
	// a typical case rather than a misconfiguration.
	s.NoError(telemetry.Flush(context.Background()))
}

func (s *FlushTestSuite) Test_what_was_installed_is_what_is_asked() {
	asked := 0
	telemetry.SetFlusher(func(context.Context) error {
		asked++
		return nil
	})

	s.Require().NoError(telemetry.Flush(context.Background()))
	s.Equal(1, asked)
}

func (s *FlushTestSuite) Test_a_failure_to_hand_over_is_reported() {
	// So that a shutdown can say the traces did not reach the collector rather
	// than appearing to have exported them.
	refused := errors.New("the collector refused")
	telemetry.SetFlusher(func(context.Context) error {
		return refused
	})

	s.ErrorIs(telemetry.Flush(context.Background()), refused)
}

func (s *FlushTestSuite) Test_installing_one_replaces_what_was_there() {
	// An application installing its own after the generated import ran is the
	// reason this is replaceable at all.
	telemetry.SetFlusher(func(context.Context) error {
		return errors.New("first")
	})

	second := 0
	telemetry.SetFlusher(func(context.Context) error {
		second++
		return nil
	})

	s.Require().NoError(telemetry.Flush(context.Background()))
	s.Equal(1, second)
}
