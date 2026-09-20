package celerity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// Environment variables that decide how an application runs.
//
// No provider's own variables appear here. Whether this process is running on a
// serverless platform is a question the linked adapters answer through
// [serverless.Detected], so core never learns the name of one.
const (
	runtimeSocketEnvVar   = "CELERITY_RUNTIME_SOCKET"
	runtimeCallModeEnvVar = "CELERITY_RUNTIME_CALL_MODE"
	extractManifestEnvVar = "CELERITY_EXTRACT_MANIFEST"
)

// Mode is how an application was asked to run.
type Mode string

const (
	// ModeRuntime serves the Celerity runtime over the IPC stream.
	ModeRuntime Mode = "runtime"
	// ModeServerless hands off to a provider's serverless event loop.
	ModeServerless Mode = "serverless"
	// ModeExtract prints the handler manifest and exits.
	ModeExtract Mode = "extract"
)

// Run starts the application, choosing how from the environment, and blocks
// until it is shut down.
//
// The mode is taken from the environment rather than chosen by the developer so
// that one binary serves every deployment target: the same artefact runs under
// the runtime, under Lambda, and under the CLI's extraction step.
func Run(app *App) {
	if err := RunContext(signalContext(), app); err != nil {
		fmt.Fprintf(os.Stderr, "celerity: %v\n", err)
		os.Exit(1)
	}
}

// RunContext is [Run] with a caller-supplied context, for tests and for
// applications that own their own shutdown signalling.
func RunContext(ctx context.Context, app *App) error {
	if err := app.Err(); err != nil {
		return fmt.Errorf("registration failed:\n%w", err)
	}
	if app.registry.Len() == 0 {
		return errors.New("no handlers registered")
	}

	switch DetectMode() {
	case ModeExtract:
		return app.writeManifest(os.Stdout)
	case ModeServerless:
		adapter, err := app.adapter()
		if err != nil {
			return err
		}
		return serverless.Serve(ctx, adapter, app.Resolver())
	default:
		return app.serveRuntime(ctx)
	}
}

// DetectMode reports how the process was asked to run.
//
// The runtime's own variables are checked first: a handler process started by
// the runtime is told so explicitly, whereas a serverless platform is inferred.
func DetectMode() Mode {
	if os.Getenv(extractManifestEnvVar) != "" {
		return ModeExtract
	}
	if os.Getenv(runtimeSocketEnvVar) != "" || os.Getenv(runtimeCallModeEnvVar) == "ipc" {
		return ModeRuntime
	}
	if _, ok := serverless.Detected(); ok {
		return ModeServerless
	}
	return ModeRuntime
}

// adapter returns the adapter to serve with: one given explicitly, which is
// what a test supplying a fake wants, or otherwise the linked one whose
// platform this process is running on.
func (a *App) adapter() (serverless.Adapter, error) {
	if a.options.adapter != nil {
		return a.options.adapter, nil
	}
	if adapter, ok := serverless.Detected(); ok {
		return adapter, nil
	}
	return nil, &serverless.MissingAdapterError{Linked: serverless.Registered()}
}

// signalContext cancels on the signals a supervisor uses to roll a process.
// SIGTERM is what starts a drain, so the IPC client answers it by telling the
// runtime it is going away rather than by dropping in-flight work.
func signalContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	_ = stop
	return ctx
}

// Resolver adapts the registry to what a serverless adapter resolves through.
//
// [Run] uses it, and an application that owns its own entry point rather than
// handing control to Run passes it to [serverless.Serve] or
// [serverless.NewInvoker] itself.
func (a *App) Resolver() serverless.Resolver { return &registryResolver{app: a} }

type registryResolver struct{ app *App }

func (r *registryResolver) ByName(name string) (*serverless.Handler, bool) {
	reg, ok := r.app.registry.ByName(name)
	if !ok {
		return nil, false
	}
	return r.app.toServerlessHandler(reg), true
}

func (r *registryResolver) ByTag(tag string) (*serverless.Handler, bool) {
	reg, ok := r.app.registry.Get(tag)
	if !ok {
		return nil, false
	}
	return r.app.toServerlessHandler(reg), true
}

func (r *registryResolver) Only(kind handler.Kind) (*serverless.Handler, bool) {
	matched := r.app.registry.OfKind(kind)
	if len(matched) != 1 {
		return nil, false
	}
	return r.app.toServerlessHandler(matched[0]), true
}
