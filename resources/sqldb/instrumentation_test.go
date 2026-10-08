package sqldb_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

// The seam a provider opens a pool through, which is what lets a module trace
// the statements without core depending on a tracing library.
type InstrumentationTestSuite struct {
	suite.Suite
}

func TestInstrumentationTestSuite(t *testing.T) {
	suite.Run(t, new(InstrumentationTestSuite))
}

func (s *InstrumentationTestSuite) TearDownTest() {
	// Process-wide, so a test that registered one puts it back.
	sqldb.Instrument(nil)
}

func (s *InstrumentationTestSuite) Test_without_a_hook_the_pool_is_database_sqls_own() {
	db, err := sqldb.Open(stubConnector{}, sqldb.EnginePostgres)

	s.Require().NoError(err)
	s.Require().NotNil(db)
	s.NoError(db.Close())
}

func (s *InstrumentationTestSuite) Test_a_hook_opens_the_pool_a_provider_queries_through() {
	// Instrumenting database/sql means wrapping the driver, so the hook is
	// given the connector rather than an open pool: there is nothing to set on
	// a pool that is already open.
	var gotEngine string
	instrumented := sql.OpenDB(stubConnector{})
	sqldb.Instrument(func(c driver.Connector, engine string) (*sql.DB, error) {
		gotEngine = engine
		return instrumented, nil
	})

	db, err := sqldb.Open(stubConnector{}, sqldb.EngineMySQL)

	s.Require().NoError(err)
	s.Same(instrumented, db, "the pool a provider queries through is the hook's")
	s.Equal(sqldb.EngineMySQL, gotEngine, "and it was told which engine to say spans are about")
	s.NoError(db.Close())
}

func (s *InstrumentationTestSuite) Test_a_hook_that_cannot_open_a_pool_says_so() {
	// Rather than falling back silently: an application that linked the module
	// and is not being traced has a problem worth hearing about.
	failed := errors.New("no exporter configured")
	sqldb.Instrument(func(driver.Connector, string) (*sql.DB, error) {
		return nil, failed
	})

	_, err := sqldb.Open(stubConnector{}, sqldb.EnginePostgres)

	s.ErrorIs(err, failed)
}

// stubConnector stands in for a driver, since what these cases are about is
// which pool comes back rather than anything a database does.
type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("celeritytest: nothing to connect to")
}

func (stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("celeritytest: nothing to open")
}
