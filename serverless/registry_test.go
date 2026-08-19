package serverless_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// Selecting an adapter is an import rather than something an application wires
// up, so these cases pin down what a linked adapter does at startup.

type fakeAdapter struct {
	name     string
	detected bool
}

func (a *fakeAdapter) Name() string                   { return a.name }
func (a *fakeAdapter) Detect() bool                   { return a.detected }
func (a *fakeAdapter) Mapper() serverless.EventMapper { return nil }

func (a *fakeAdapter) Start(context.Context, serverless.Invoker) error { return nil }

func TestRegisteredReportsWhatIsLinked(t *testing.T) {
	serverless.Register(&fakeAdapter{name: "test-linked"})

	if !contains(serverless.Registered(), "test-linked") {
		t.Errorf("Registered() = %v, want it to contain test-linked", serverless.Registered())
	}
}

func TestDetectedPicksTheAdapterForThisEnvironment(t *testing.T) {
	// Several adapters may be linked into one binary, which is what lets a
	// single artefact target more than one platform.
	serverless.Register(&fakeAdapter{name: "test-elsewhere", detected: false})
	serverless.Register(&fakeAdapter{name: "test-here", detected: true})

	adapter, ok := serverless.Detected()
	if !ok {
		t.Fatal("Detected() found no adapter, want test-here")
	}
	if adapter.Name() != "test-here" {
		t.Errorf("Detected() = %q, want test-here", adapter.Name())
	}
}

func TestRegisterRefusesADuplicateName(t *testing.T) {
	serverless.Register(&fakeAdapter{name: "test-duplicate"})

	defer func() {
		if recover() == nil {
			t.Error("registering two adapters under one name did not panic")
		}
	}()
	serverless.Register(&fakeAdapter{name: "test-duplicate"})
}

func TestMissingAdapterErrorNamesTheImportThatFixesIt(t *testing.T) {
	err := error(&serverless.MissingAdapterError{Linked: []string{"gcp-functions"}})

	var missing *serverless.MissingAdapterError
	if !errors.As(err, &missing) {
		t.Fatalf("errors.As did not match, err = %v", err)
	}

	message := err.Error()
	for _, want := range []string{"import _", "serverless/aws", "gcp-functions"} {
		if !strings.Contains(message, want) {
			t.Errorf("error message does not mention %q:\n%s", want, message)
		}
	}
}

// The vocabulary an adapter maps into is the same one the runtime dispatches
// in, which is what lets one handler serve both.
var _ = handler.KindHTTP

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
