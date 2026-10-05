//go:build integration

// The database client against real PostgreSQL and MySQL.
//
// What a unit test mock cannot establish: that the connection string built from what a
// deployment recorded actually connects, which includes the engine, the driver,
// the escaping and the pool.
//
// Run with: bash scripts/run-tests.sh --with-integration
package sqldb_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsresources "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

type SQLIntegrationTestSuite struct {
	suite.Suite

	provider *awsresources.Provider
}

func TestSQLIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(SQLIntegrationTestSuite))
}

func (s *SQLIntegrationTestSuite) SetupSuite() {
	s.provider = awsresources.New()
}

func (s *SQLIntegrationTestSuite) Test_a_handler_writes_and_reads_through_the_connection_it_is_given() {
	// Everything resolved from the deployment has to be right for this to
	// connect at all: the engine, the driver, the connection string and the
	// escaping in it.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			writer, err := s.database(engine).Writer(ctx)
			s.Require().NoError(err)
			defer writer.Close()

			_, err = writer.ExecContext(ctx,
				`create table if not exists orders (id int primary key, total int)`)
			s.Require().NoError(err)
			_, err = writer.ExecContext(ctx, `delete from orders`)
			s.Require().NoError(err)

			result, err := writer.ExecContext(ctx,
				`insert into orders values (`+placeholders(engine, 2)+`)`, 1, 42)
			s.Require().NoError(err)
			affected, err := result.RowsAffected()
			s.Require().NoError(err)
			s.EqualValues(1, affected)

			rows, err := writer.QueryContext(ctx, `select id, total from orders order by id`)
			s.Require().NoError(err)
			defer rows.Close()

			var found [][2]int
			for rows.Next() {
				var id, total int
				s.Require().NoError(rows.Scan(&id, &total))
				found = append(found, [2]int{id, total})
			}
			s.Require().NoError(rows.Err())
			s.Equal([][2]int{{1, 42}}, found)
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_reader_is_the_writer_where_no_replica_was_deployed() {
	// Whether a cluster has a replica is a property of the deployment and not
	// of the query, so a handler asking to read should not have to know which
	// environment it is in.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()

			reader, err := s.database(engine).Reader(ctx)
			s.Require().NoError(err)
			defer reader.Close()

			rows, err := reader.QueryContext(ctx, `select 1`)
			s.Require().NoError(err)
			s.Require().NoError(rows.Close())
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_closing_a_connection_returns_it_rather_than_shutting_the_pool() {
	// A handler closing what it was given is the ordinary thing to do, and it
	// must not cost the next invocation its connections.
	ctx := context.Background()
	database := s.database(sqldb.EnginePostgres)

	first, err := database.Writer(ctx)
	s.Require().NoError(err)
	s.Require().NoError(first.Close())

	second, err := database.Writer(ctx)
	s.Require().NoError(err)
	defer second.Close()

	rows, err := second.QueryContext(ctx, `select 1`)
	s.Require().NoError(err)
	s.Require().NoError(rows.Close())
}

func (s *SQLIntegrationTestSuite) Test_a_committed_transaction_takes_effect_all_at_once() {
	// Only a real database can be held to this: a transaction is the database
	// keeping its own promise, and a stand-in agrees with whatever it is told.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()

			tx, err := conn.BeginTx(ctx)
			s.Require().NoError(err)
			defer tx.Rollback()

			for id, total := range map[int]int{1: 42, 2: 43} {
				_, err = tx.ExecContext(ctx,
					`insert into orders values (`+placeholders(engine, 2)+`)`, id, total)
				s.Require().NoError(err)
			}
			s.Require().NoError(tx.Commit())

			s.Equal(2, s.count(ctx, conn), "both rows should be there")
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_rolled_back_transaction_leaves_nothing_behind() {
	// What a transaction is for: a handler that fails half way through should
	// not leave half of its work committed.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()

			tx, err := conn.BeginTx(ctx)
			s.Require().NoError(err)

			_, err = tx.ExecContext(ctx,
				`insert into orders values (`+placeholders(engine, 2)+`)`, 1, 42)
			s.Require().NoError(err)
			s.Require().NoError(tx.Rollback())

			s.Equal(0, s.count(ctx, conn), "the insert should have been undone")
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_rollback_after_a_commit_reports_a_finished_transaction() {
	// What makes a deferred rollback the way to be sure a transaction is always
	// finished: after a commit it does nothing and says so.
	ctx := context.Background()
	conn := s.prepared(ctx, sqldb.EnginePostgres)
	defer conn.Close()

	tx, err := conn.BeginTx(ctx)
	s.Require().NoError(err)
	s.Require().NoError(tx.Commit())

	err = tx.Rollback()

	s.Require().Error(err)
	s.ErrorIs(err, sqldb.ErrTxDone)
}

func (s *SQLIntegrationTestSuite) Test_a_transaction_reads_what_it_has_written_before_committing() {
	// Nothing a transaction does is visible outside it until it commits, but
	// all of it is visible inside.
	ctx := context.Background()
	conn := s.prepared(ctx, sqldb.EnginePostgres)
	defer conn.Close()

	tx, err := conn.BeginTx(ctx)
	s.Require().NoError(err)
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`insert into orders values (`+placeholders(sqldb.EnginePostgres, 2)+`)`, 1, 42)
	s.Require().NoError(err)

	rows, err := tx.QueryContext(ctx, `select total from orders where id = 1`)
	s.Require().NoError(err)
	defer rows.Close()
	s.Require().True(rows.Next())
	var total int
	s.Require().NoError(rows.Scan(&total))
	s.Equal(42, total)
	s.Require().NoError(rows.Err())
}

func (s *SQLIntegrationTestSuite) Test_the_isolation_a_transaction_asks_for_is_accepted() {
	// Only the portable levels are named, so every one of them has to be a
	// level both engines run as itself rather than refuse or quietly rename.
	levels := map[string]sqldb.IsolationLevel{
		"default":         sqldb.LevelDefault,
		"read committed":  sqldb.LevelReadCommitted,
		"repeatable read": sqldb.LevelRepeatableRead,
		"serializable":    sqldb.LevelSerializable,
	}

	for _, engine := range engines() {
		for name, level := range levels {
			s.Run(engine+" "+name, func() {
				ctx := context.Background()
				conn := s.prepared(ctx, engine)
				defer conn.Close()

				tx, err := conn.BeginTx(ctx, func(o *sqldb.TxOptions) { o.Isolation = level })
				s.Require().NoError(err)
				defer tx.Rollback()

				rows, err := tx.QueryContext(ctx, `select 1`)
				s.Require().NoError(err)
				s.Require().NoError(rows.Close())
				s.Require().NoError(tx.Commit())
			})
		}
	}
}

func (s *SQLIntegrationTestSuite) Test_a_read_only_transaction_is_refused_a_write() {
	// The declaration is what lets a database answer from a replica, so it has
	// to be one the database actually holds the transaction to.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()

			tx, err := conn.BeginTx(ctx, func(o *sqldb.TxOptions) { o.ReadOnly = true })
			s.Require().NoError(err)
			defer tx.Rollback()

			_, err = tx.ExecContext(ctx,
				`insert into orders values (`+placeholders(engine, 2)+`)`, 1, 42)

			s.Require().Error(err, "a read-only transaction should not be able to write")
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_single_row_query_scans_without_a_result_set_to_close() {
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()

			_, err := conn.ExecContext(ctx,
				`insert into orders values (`+placeholders(engine, 2)+`)`, 1, 42)
			s.Require().NoError(err)

			var total int
			err = conn.QueryRowContext(ctx,
				`select total from orders where id = `+placeholders(engine, 1), 1).Scan(&total)

			s.Require().NoError(err)
			s.Equal(42, total)
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_single_row_query_that_matched_nothing_says_so() {
	// Reported rather than left as a zero value, so that no such row and a row
	// of zeroes can be told apart.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()

			var total int
			err := conn.QueryRowContext(ctx,
				`select total from orders where id = `+placeholders(engine, 1), 404).Scan(&total)

			s.Require().Error(err)
			s.ErrorIs(err, sqldb.ErrNoRows)
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_single_row_query_runs_inside_a_transaction() {
	ctx := context.Background()
	conn := s.prepared(ctx, sqldb.EnginePostgres)
	defer conn.Close()

	tx, err := conn.BeginTx(ctx)
	s.Require().NoError(err)
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`insert into orders values (`+placeholders(sqldb.EnginePostgres, 2)+`)`, 1, 42)
	s.Require().NoError(err)

	var total int
	err = tx.QueryRowContext(ctx, `select total from orders where id = $1`, 1).Scan(&total)

	s.Require().NoError(err)
	s.Equal(42, total, "a transaction reads what it has written")
}

func (s *SQLIntegrationTestSuite) Test_a_prepared_statement_runs_many_times() {
	// What a prepared statement is for: one parse and plan, then the arguments
	// change per call.
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()

			insert, err := conn.PrepareContext(ctx,
				`insert into orders values (`+placeholders(engine, 2)+`)`)
			s.Require().NoError(err)
			defer insert.Close()

			for id := 1; id <= 3; id++ {
				result, err := insert.ExecContext(ctx, id, id*10)
				s.Require().NoError(err)
				affected, err := result.RowsAffected()
				s.Require().NoError(err)
				s.EqualValues(1, affected)
			}

			s.Equal(3, s.count(ctx, conn))
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_prepared_statement_reads_rows_and_one_row() {
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn := s.prepared(ctx, engine)
			defer conn.Close()
			_, err := conn.ExecContext(ctx,
				`insert into orders values (`+placeholders(engine, 2)+`)`, 1, 42)
			s.Require().NoError(err)

			byID, err := conn.PrepareContext(ctx,
				`select total from orders where id = `+placeholders(engine, 1))
			s.Require().NoError(err)
			defer byID.Close()

			var total int
			s.Require().NoError(byID.QueryRowContext(ctx, 1).Scan(&total))
			s.Equal(42, total)

			rows, err := byID.QueryContext(ctx, 1)
			s.Require().NoError(err)
			defer rows.Close()
			s.Require().True(rows.Next())
			s.Require().NoError(rows.Scan(&total))
			s.Equal(42, total)
			s.Require().NoError(rows.Err())
		})
	}
}

func (s *SQLIntegrationTestSuite) Test_a_statement_prepared_in_a_transaction_is_the_transactions() {
	// It cannot outlive the transaction, which is why it is prepared on the
	// transaction rather than on the connection under it.
	ctx := context.Background()
	conn := s.prepared(ctx, sqldb.EnginePostgres)
	defer conn.Close()

	tx, err := conn.BeginTx(ctx)
	s.Require().NoError(err)
	defer tx.Rollback()

	insert, err := tx.PrepareContext(ctx, `insert into orders values ($1, $2)`)
	s.Require().NoError(err)
	_, err = insert.ExecContext(ctx, 1, 42)
	s.Require().NoError(err)
	s.Require().NoError(tx.Commit())

	s.Equal(1, s.count(ctx, conn), "what the transaction prepared and ran took effect")

	_, err = insert.ExecContext(ctx, 2, 43)
	s.Require().Error(err, "the statement should not outlive the transaction")
}

func (s *SQLIntegrationTestSuite) Test_a_statement_the_database_refuses_is_refused_on_preparing() {
	// Preparing is where a query is parsed, so a query that will never run is
	// reported here rather than on the first call.
	ctx := context.Background()
	conn := s.prepared(ctx, sqldb.EnginePostgres)
	defer conn.Close()

	_, err := conn.PrepareContext(ctx, `select nonexistent from nowhere`)

	s.Require().Error(err)
}

func (s *SQLIntegrationTestSuite) Test_a_reachable_database_answers_a_ping() {
	for _, engine := range engines() {
		s.Run(engine, func() {
			ctx := context.Background()
			conn, err := s.database(engine).Writer(ctx)
			s.Require().NoError(err)
			defer conn.Close()

			s.Require().NoError(conn.PingContext(ctx))
		})
	}
}

// prepared hands back a writer with an empty orders table, which is what every
// transaction test starts from.
func (s *SQLIntegrationTestSuite) prepared(ctx context.Context, engine string) sqldb.Conn {
	conn, err := s.database(engine).Writer(ctx)
	s.Require().NoError(err)

	_, err = conn.ExecContext(ctx,
		`create table if not exists orders (id int primary key, total int)`)
	s.Require().NoError(err)
	_, err = conn.ExecContext(ctx, `delete from orders`)
	s.Require().NoError(err)
	return conn
}

func (s *SQLIntegrationTestSuite) count(ctx context.Context, conn sqldb.Conn) int {
	rows, err := conn.QueryContext(ctx, `select count(*) from orders`)
	s.Require().NoError(err)
	defer rows.Close()

	s.Require().True(rows.Next())
	var found int
	s.Require().NoError(rows.Scan(&found))
	s.Require().NoError(rows.Err())
	return found
}

func (s *SQLIntegrationTestSuite) database(engine string) sqldb.Client {
	port, name := "CELERITY_TEST_POSTGRES_PORT", "5432"
	if engine == sqldb.EngineMySQL {
		port, name = "CELERITY_TEST_MYSQL_PORT", "3306"
	}

	client, err := s.provider.SQLDatabase(awstest.Ref(resources.KindSQLDatabase, "ordersDb",
		map[string]string{
			"ordersDb_host":     "127.0.0.1",
			"ordersDb_port":     portOr(port, name),
			"ordersDb_user":     "orders_app",
			"ordersDb_password": "local-password",
			"ordersDb_database": "ordersdb",
			"ordersDb_engine":   engine,
			"ordersDb_ssl":      "false",
		}))
	s.Require().NoError(err)
	return client
}

func engines() []string {
	return []string{sqldb.EnginePostgres, sqldb.EngineMySQL}
}

func portOr(name, fallback string) string {
	if port := os.Getenv(name); port != "" {
		return port
	}
	return fallback
}

// placeholders is the one thing the two engines spell differently that a test
// has to care about. The SDK hands the query through as it was written, which
// is what keeps it out of the business of dialects.
func placeholders(engine string, n int) string {
	marks := make([]string, n)
	for i := range marks {
		if engine == sqldb.EngineMySQL {
			marks[i] = "?"
			continue
		}
		marks[i] = "$" + strconv.Itoa(i+1)
	}
	return strings.Join(marks, ", ")
}
