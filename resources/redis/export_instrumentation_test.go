package redis

// Exported for the instrumentation suite, which drives the seam directly, a
// hook is run when a client is built, and building one needs a cache to reach.
var (
	ApplyInstrumentation = instrument
	ClearInstrumentation = func() {
		instrumentation = nil
	}
)
