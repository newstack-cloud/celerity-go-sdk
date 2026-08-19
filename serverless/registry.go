package serverless

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Adapters register themselves from an init function, so that selecting one is
// an import rather than something an application wires up:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
//
// This is the driver-registration pattern database/sql and image/png use, and
// it is as close as an ahead-of-time compiled language gets to what the Node
// and Python SDKs do. There, a deployment points at an entry point the SDK
// ships and the SDK loads the application; Go links one binary, so the import
// is what puts the adapter in it, and which one runs is still decided at
// startup from the environment rather than by the application.

var (
	registryMu sync.RWMutex
	registered []Adapter
)

// Register adds an adapter to the set considered at startup.
//
// It is called from an adapter package's init function and panics on a
// duplicate name, which can only mean two adapters claiming one platform.
func Register(adapter Adapter) {
	registryMu.Lock()
	defer registryMu.Unlock()

	for _, existing := range registered {
		if existing.Name() == adapter.Name() {
			panic(fmt.Sprintf("celerity: serverless adapter %q is registered twice", adapter.Name()))
		}
	}
	registered = append(registered, adapter)
}

// Registered returns every linked adapter, by name and sorted.
//
// The handler manifest reports this, so that a deployment targeting a platform
// the binary has no adapter for is caught when the application is built rather
// than when it is first invoked.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registered))
	for _, adapter := range registered {
		names = append(names, adapter.Name())
	}
	sort.Strings(names)
	return names
}

// Detected returns the adapter whose platform this process is running on.
//
// Several adapters may be linked into one binary, which is what lets a single
// artefact target more than one platform; at most one of them detects its own
// environment.
func Detected() (Adapter, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	for _, adapter := range registered {
		if adapter.Detect() {
			return adapter, true
		}
	}
	return nil, false
}

// MissingAdapterError reports that the process is running under a serverless
// platform that no linked adapter serves.
//
// It names the import that would fix it, because the fix is always an import
// and guessing which one is not the developer's job.
type MissingAdapterError struct {
	// Linked names the adapters that are compiled in, which is commonly empty
	// and is the useful half of the message when it is not.
	Linked []string
}

func (e *MissingAdapterError) Error() string {
	var b strings.Builder
	b.WriteString("running in a serverless environment that no linked adapter serves.\n")
	b.WriteString("Import the adapter for the platform being deployed to, for example:\n\n")
	b.WriteString("\timport _ \"github.com/newstack-cloud/celerity-go-sdk/serverless/aws\"\n")

	if len(e.Linked) > 0 {
		b.WriteString("\nAdapters linked into this binary: ")
		b.WriteString(strings.Join(e.Linked, ", "))
	} else {
		b.WriteString("\nNo serverless adapters are linked into this binary.")
	}
	return b.String()
}
