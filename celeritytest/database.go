package celeritytest

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

// refusingDatabase is what a handler reaches where a test arranged no database.
//
// There is no double for a relational database, because the part of a handler
// a double would have to check is the statement it sends, and only an engine
// can say whether that statement is right. Anything in process either
// reimplements an engine or records that a connection was asked for, and a test
// that passes because a connection was asked for is a test that would pass with
// the query misspelt.
//
// So the harness refuses instead of recording, and names the way to test it:
// a real engine, which a development session already runs.
type refusingDatabase struct {
	name string
}

func (d refusingDatabase) Writer(context.Context) (sqldb.Conn, error) {
	return nil, d.refuse("Writer")
}

func (d refusingDatabase) Reader(context.Context) (sqldb.Conn, error) {
	return nil, d.refuse("Reader")
}

func (d refusingDatabase) refuse(call string) error {
	return fmt.Errorf(
		"celeritytest: %s was asked for %s, and a SQL database has no in-memory double: "+
			"a statement is only right or wrong against an engine. Run one, which a "+
			"celerity dev session does, and hand it over with "+
			"Resources().WithDatabase(%q, client)",
		d.name, call, d.name)
}
