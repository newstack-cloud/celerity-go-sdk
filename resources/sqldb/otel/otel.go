// Package otel traces the statements a Celerity application runs against its
// database.
//
// It is selected by importing it, which `celerity-go generate --telemetry otel`
// writes alongside the tracer itself:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/sqldb/otel"
//
// # What it adds
//
// The resources package traces taking a connection and nothing else, because a
// statement's text is the application's rather than the SDK's. This traces the
// statements: one span per query, with how long it took and whether a
// connection came from the pool or had to be opened. Which is where a handler
// that is slow because the pool is exhausted becomes visible, since from above
// it is one slow handler.
//
// # A module of its own
//
// An application with no database links neither this nor what it depends on,
// and one that has a database but exports no traces links neither either. So
// core offers the seam and this fills it, from an init, which is why the
// generated file is a blank import rather than wiring.
package otel

import (
	"database/sql"
	"database/sql/driver"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

func init() {
	sqldb.Instrument(Open)
}

// Open returns a traced pool over a connector.
//
// The engine names the system the spans are about, in OpenTelemetry's own
// vocabulary rather than Celerity's, since that is what a backend groups
// database spans by.
func Open(c driver.Connector, engine string) (*sql.DB, error) {
	return otelsql.OpenDB(c, otelsql.WithAttributes(systemFor(engine))), nil
}

func systemFor(engine string) attribute.KeyValue {
	if engine == sqldb.EngineMySQL {
		return semconv.DBSystemMySQL
	}
	return semconv.DBSystemPostgreSQL
}
