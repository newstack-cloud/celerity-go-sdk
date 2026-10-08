package redis

import goredis "github.com/redis/go-redis/v9"

// Instrumentation is given a client before anything is sent through it.
type Instrumentation func(goredis.UniversalClient) error

var instrumentation []Instrumentation

// Instrument registers a hook run against every client this package builds.
//
// Called from the init of the module holding it, so that linking the module is
// the whole of the wiring. More than one is allowed, run in the order they
// were registered, since tracing and metrics are commonly separate hooks.
func Instrument(hook Instrumentation) {
	instrumentation = append(instrumentation, hook)
}

// instrument applies the registered hooks, stopping at the first that fails.
//
// A hook that cannot instrument a client is reported rather than ignored, an
// application that linked the module and is not being traced has a problem
// worth knowing about, and the alternative is silence.
func instrument(client goredis.UniversalClient) error {
	for _, hook := range instrumentation {
		if err := hook(client); err != nil {
			return err
		}
	}

	return nil
}
