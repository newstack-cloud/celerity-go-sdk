package serverless_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// Selecting an adapter is an import rather than something an application wires
// up, so these cases pin down what a linked adapter does at startup.
type AdapterRegistryTestSuite struct {
	suite.Suite
}

func TestAdapterRegistryTestSuite(t *testing.T) {
	suite.Run(t, new(AdapterRegistryTestSuite))
}

type fakeAdapter struct {
	name     string
	detected bool
}

func (a *fakeAdapter) Name() string                                    { return a.name }
func (a *fakeAdapter) Detect() bool                                    { return a.detected }
func (a *fakeAdapter) Mapper() serverless.EventMapper                  { return nil }
func (a *fakeAdapter) Start(context.Context, serverless.Invoker) error { return nil }

func (s *AdapterRegistryTestSuite) Test_registered_reports_what_is_linked() {
	serverless.Register(&fakeAdapter{name: "test-linked"})

	s.Contains(serverless.Registered(), "test-linked")
}

func (s *AdapterRegistryTestSuite) Test_detected_picks_the_adapter_for_this_environment() {
	// Several adapters may be linked into one binary, which is what lets a
	// single artefact target more than one platform.
	serverless.Register(&fakeAdapter{name: "test-elsewhere", detected: false})
	serverless.Register(&fakeAdapter{name: "test-here", detected: true})

	adapter, ok := serverless.Detected()

	s.Require().True(ok, "no adapter detected, want test-here")
	s.Equal("test-here", adapter.Name())
}

func (s *AdapterRegistryTestSuite) Test_registering_two_adapters_under_one_name_panics() {
	serverless.Register(&fakeAdapter{name: "test-duplicate"})

	s.Panics(func() {
		serverless.Register(&fakeAdapter{name: "test-duplicate"})
	})
}

func (s *AdapterRegistryTestSuite) Test_a_missing_adapter_names_the_import_that_fixes_it() {
	err := error(&serverless.MissingAdapterError{Linked: []string{"gcp-functions"}})

	var missing *serverless.MissingAdapterError
	s.Require().ErrorAs(err, &missing)

	// The fix is always an import, and guessing which one is not the
	// developer's job, so the message carries it along with what is linked.
	s.Contains(err.Error(), "import _")
	s.Contains(err.Error(), "serverless/aws")
	s.Contains(err.Error(), "gcp-functions")
}

// The vocabulary an adapter maps into is the same one the runtime dispatches
// in, which is what lets one handler serve both.
var _ = handler.KindHTTP
