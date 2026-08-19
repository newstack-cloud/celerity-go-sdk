package celerity

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/internal/ipc"
	"github.com/newstack-cloud/celerity-go-sdk/layer"
)

// Version is the SDK version declared to the runtime in the handshake.
const Version = "0.1.0"

// serveRuntime connects to the Celerity runtime and serves its dispatch loop
// until the runtime drains or the context is cancelled.
func (a *App) serveRuntime(ctx context.Context) error {
	transport, err := ipc.Dial(ctx, ipc.DialConfigFromEnv())
	if err != nil {
		return err
	}

	client := ipc.New(transport, ipc.Config{
		Tags:        a.registry.Tags(),
		Resolve:     a.resolvePipeline,
		Concurrency: a.options.concurrency,
		Limits:      a.options.handlerLimits,
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
