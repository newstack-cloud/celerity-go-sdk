//go:build integration

// The driver an application reaches a database through is linked by the build
// rather than by the SDK, so the only place this can be checked is a build that
// links one.
package sqldb_test

import (
	"database/sql"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	awssqldb "github.com/newstack-cloud/celerity-go-sdk/resources/aws/sqldb"
)

func TestADatabaseIsReachedWithADriverTheBuildLinked(t *testing.T) {
	// This test imports both drivers and the SDK imports neither, which is the
	// whole arrangement: celerity-go generate writes the import for the engine
	// the blueprint declares, so an application with no database carries none.
	for _, engine := range []string{"postgres", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			name, err := awssqldb.DriverFor(engine)

			require.NoError(t, err)
			assert.Contains(t, sql.Drivers(), name)
		})
	}
}
