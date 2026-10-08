package sqldb

import (
	"database/sql"
	"database/sql/driver"
)

// Instrumentation opens the pool a provider will query through, from the
// connector it would have opened one from.
//
// It takes a connector rather than an open pool because instrumenting
// database/sql means wrapping the driver: there is nothing to set on a pool
// that is already open, so a hook has to be given what the pool would be built
// from. Which is why a provider calls [Open] instead of database/sql's own
// OpenDB.
//
// Declared here rather than in a provider module because a connector is
// database/sql's whoever built it, so one hook serves every provider. What
// fills it is a module linked for the purpose, since core depends on no tracing
// library.
type Instrumentation func(c driver.Connector, engine string) (*sql.DB, error)

var instrumentation Instrumentation

// Instrument registers the hook pools are opened through, replacing any
// already registered.
//
// One rather than many, unlike the hooks a client is handed after it is built:
// each of these opens the pool, and two of them would open two.
//
// Called from the init of the module holding it, so that linking the module is
// the whole of the wiring.
func Instrument(hook Instrumentation) {
	instrumentation = hook
}

// Open returns the pool to query through, instrumented where a module for it
// is linked, and database/sql's own otherwise.
//
// Called by a provider that has built a connector. Exported because the
// provider is in another module, and it is the only caller: an application has
// no reason to.
func Open(c driver.Connector, engine string) (*sql.DB, error) {
	if instrumentation == nil {
		return sql.OpenDB(c), nil
	}
	return instrumentation(c, engine)
}
