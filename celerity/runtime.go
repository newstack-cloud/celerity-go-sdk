package celerity

import (
	"context"
	"os"
	"runtime"
	"strconv"

	"github.com/newstack-cloud/celerity-go-sdk/internal/ipc"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Version is the SDK version declared to the runtime in the handshake.
const Version = "0.1.0"

// Sets how many events the runtime may have in flight to this
// process, which is the worker pool size and the initial credit the handshake
// declares.
//
// It is read here rather than taken as an application option because it only
// means anything on this path. A serverless platform decides how many events
// reach a function, so an option would have been a knob that silently did
// nothing in half of the deployments an application can have, and a deployment
// concern encoded in application source in the other half.
const concurrencyEnvVar = "CELERITY_HANDLER_CONCURRENCY"

// Returns the worker pool size.
//
// The default is the pool size throughput saturates at, every unit beyond it
// adds latency for almost no throughput, so it is not a knob worth reaching for
// without a measurement.
func handlerConcurrency() int {
	if raw := os.Getenv(concurrencyEnvVar); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return runtime.NumCPU() * 4
}

// serveRuntime connects to the Celerity runtime and serves its dispatch loop
// until the runtime drains or the context is cancelled.
func (a *App) serveRuntime(ctx context.Context) error {
	transport, err := ipc.Dial(ctx, ipc.DialConfigFromEnv())
	if err != nil {
		return err
	}

	client := ipc.New(transport, ipc.Config{
		Tags:        a.registry.Tags(),
		Reconcile:   a.reconcileFrom,
		Resolve:     a.resolvePipeline,
		Limits:      a.HandlerLimits,
		Concurrency: handlerConcurrency(),
		SDKVersion:  Version,
	})

	return client.Serve(ctx)
}

// resolvePipeline is how the IPC client reaches a handler, building the full
// pipeline once per dispatch resolution.
func (a *App) resolvePipeline(tag string) (layer.Next, bool) {
	reg, ok := a.registry.Get(tag)
	if !ok {
		return nil, false
	}
	return a.Pipeline(reg), true
}

// Adapts what the protocol reports to this package's own type, so the
// reconciliation it drives does not depend on the wire contract.
func (a *App) reconcileFrom(blueprint []ipc.HandlerConfig) ([]string, error) {
	declared := make([]BlueprintHandler, 0, len(blueprint))
	for _, h := range blueprint {
		declared = append(declared, BlueprintHandler{
			Name:          h.HandlerName,
			PublishedName: h.PublishedName,
			Tag:           h.HandlerTag,
		})
	}
	return a.ReconcileTags(declared)
}
