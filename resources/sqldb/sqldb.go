// Package sqldb is the provider-agnostic interface to a relational database:
// RDS, Cloud SQL, Azure Database.
//
// A handle is taken by naming the blueprint resource:
//
//	orders := resources.SQLDatabase(app, "ordersDb")
//
// The SDK imports no driver. Which one a build links is a deployment fact, so
// the build tool writes the import; see the sql-engine flag on celerity-go
// generate. Implementations are per provider and live in their own modules,
// such as resources/aws. Typically, implementations will use the database/sql
// driver, the [Conn] interface itself is a subset of database/sql.
package sqldb

import (
	"context"
	"database/sql"
)

// The relational engines Celerity supports, named as the blueprint names them
// and as the deploy engine records them.
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
)

// Client is a relational database, handing out separate writer and reader
// endpoints so that a read replica is used where one exists.
type Client interface {
	// Writer returns a connection to the primary, which can read and write.
	Writer(ctx context.Context) (Conn, error)
	// Reader returns a connection for reads, which is a replica where the
	// deployment has one and the primary where it does not.
	Reader(ctx context.Context) (Conn, error)
}

// Conn is the subset of database/sql a handler needs, kept as an interface so
// that a driver is never forced on a caller.
type Conn interface {
	// ExecContext runs a statement that answers with an effect rather than rows.
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	// QueryContext runs a query and returns its rows, which have to be closed.
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	// QueryRowContext runs a query expected to answer with one row.
	//
	// There is nothing to close: the row holds the result set and releases it
	// when it is scanned. A query that matched nothing reports [ErrNoRows] from
	// Scan, which is the one case where a failure to scan is not a failure.
	QueryRowContext(ctx context.Context, query string, args ...any) Row
	// PingContext checks that the database is reachable and answering.
	//
	// For a health check rather than before a query: a query that is going to
	// run anyway reports the same thing by failing.
	PingContext(ctx context.Context) error
	// PrepareContext prepares a statement on this connection, for running the
	// same query many times.
	//
	// The statement has to be closed, and belongs to this connection: it is
	// prepared on the server at the other end of it, so closing the connection
	// without closing the statement leaves the server holding it until the
	// connection is actually dropped.
	//
	// Worth reaching for only where one connection runs the same statement many
	// times: a backfill, a bulk load, a loop over a result set. What it saves is
	// the parse and plan of the query, so preparing for a single call pays an
	// extra round trip and saves nothing, and on PostgreSQL the driver caches
	// statements for itself regardless.
	PrepareContext(ctx context.Context, query string) (Stmt, error)
	// BeginTx starts a transaction, so that statements which only make sense
	// together either all take effect or none do.
	//
	// The transaction has to be finished with [Tx.Commit] or [Tx.Rollback]: it
	// holds this connection until one of them is called, and a handler that
	// returns without calling either leaves the connection held until the
	// context is cancelled.
	BeginTx(ctx context.Context, opts ...TxOption) (Tx, error)
	// Close gives the connection back to the pool it came from.
	Close() error
}

// Tx is a transaction, statements that take effect together or not at all.
//
// Nothing a transaction does is visible to anything else until it is committed,
// and a rollback reverts all of it.
type Tx interface {
	// ExecContext runs a statement within the transaction.
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	// QueryContext runs a query within the transaction. The rows have to be
	// closed, and before the transaction is finished.
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	// QueryRowContext runs a query expected to answer with one row, within the
	// transaction. See [Conn.QueryRowContext].
	QueryRowContext(ctx context.Context, query string, args ...any) Row
	// PrepareContext prepares a statement for use inside this transaction.
	//
	// The statement is the transaction's: it cannot be used after a commit or a
	// rollback, and it is closed with the transaction whether or not the caller
	// closes it.
	PrepareContext(ctx context.Context, query string) (Stmt, error)
	// Commit makes everything the transaction did take effect.
	Commit() error
	// Rollback undoes everything the transaction did.
	//
	// Safe to call after a commit, where it reports [ErrTxDone] and does
	// nothing, which is what makes a deferred rollback the way to be sure a
	// transaction is always finished:
	//
	//	tx, err := conn.BeginTx(ctx)
	//	if err != nil {
	//	    return err
	//	}
	//	defer tx.Rollback()
	//	...
	//	return tx.Commit()
	Rollback() error
}

// ErrTxDone reports a transaction that was already committed or rolled back.
//
//	if errors.Is(err, sqldb.ErrTxDone) { ... }
//
// What a deferred rollback reports after a commit, which is the one case where
// it means nothing went wrong.
var ErrTxDone = sql.ErrTxDone

// TxOption configures a transaction.
type TxOption func(*TxOptions)

// TxOptions is the resolved configuration for a transaction.
type TxOptions struct {
	// Isolation is how much of what other transactions are doing this one can
	// see. Zero leaves it to the database's own default.
	Isolation IsolationLevel
	// ReadOnly declares that the transaction writes nothing, which lets a
	// database answer it from a replica and skip the bookkeeping a write needs.
	ReadOnly bool
}

// IsolationLevel is how much of what other transactions are doing a transaction
// can see.
//
// Only the levels every supported engine implements as itself. Read
// uncommitted is absent: Postgres accepts it and runs read committed anyway, so
// asking for it would mean one thing on one engine and another on the next.
type IsolationLevel int

const (
	// LevelDefault leaves the isolation to the database's own default.
	LevelDefault IsolationLevel = IsolationLevel(sql.LevelDefault)
	// LevelReadCommitted sees only what other transactions have committed, as
	// of each statement.
	LevelReadCommitted = IsolationLevel(sql.LevelReadCommitted)
	// LevelRepeatableRead sees the same rows throughout, so a row read twice
	// reads the same both times.
	LevelRepeatableRead = IsolationLevel(sql.LevelRepeatableRead)
	// LevelSerializable runs as though no other transaction were running, and
	// may be refused rather than allowed to produce a result that could not
	// have happened in some order.
	LevelSerializable = IsolationLevel(sql.LevelSerializable)
)

// ResolveTxOptions applies options in order and returns what they amount to.
//
// For provider modules, which read the options to start a transaction. An
// application has no reason to call it.
func ResolveTxOptions(opts []TxOption) TxOptions {
	var options TxOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// Result reports the effect of a statement.
type Result interface {
	// RowsAffected is how many rows the statement changed.
	RowsAffected() (int64, error)
	// LastInsertId is the id the statement generated, where the engine reports
	// one. MySQL does; PostgreSQL answers a RETURNING clause instead.
	LastInsertId() (int64, error)
}

// Stmt is a statement the database has already parsed and planned, for running
// the same query many times without paying for that again.
//
// Takes no query: the statement is the query, and the arguments are what
// changes between calls.
type Stmt interface {
	// ExecContext runs the statement with these arguments.
	ExecContext(ctx context.Context, args ...any) (Result, error)
	// QueryContext runs the statement and returns its rows, which have to be
	// closed.
	QueryContext(ctx context.Context, args ...any) (Rows, error)
	// QueryRowContext runs the statement expecting one row. See
	// [Conn.QueryRowContext].
	QueryRowContext(ctx context.Context, args ...any) Row
	// Close gives the statement back. A statement left open is held by the
	// server as well as by this process.
	Close() error
}

// Row is a result expected to hold one row.
type Row interface {
	// Scan copies the row's columns into dest, and reports [ErrNoRows] where
	// the query matched nothing.
	Scan(dest ...any) error
	// Err reports a failure that happened before there was a row to scan.
	Err() error
}

// ErrNoRows reports a single-row query that matched nothing.
//
//	if errors.Is(err, sqldb.ErrNoRows) { ... }
//
// Reported by [Row.Scan] rather than answered with a zero value, so a missing
// row and a row whose columns are all zero are told apart.
var ErrNoRows = sql.ErrNoRows

// Rows iterates a result set, and has to be closed.
type Rows interface {
	// Next advances to the next row, and is false at the end of the set or on a
	// failure, which [Rows.Err] tells apart.
	Next() bool
	// Scan copies the current row's columns into dest.
	Scan(dest ...any) error
	// Err reports what stopped the iteration, where anything did.
	Err() error
	// Close gives the result set back.
	Close() error
}
