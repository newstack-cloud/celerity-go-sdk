package sqldb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

// Stated here rather than left to the builder that returns one, so that an
// operation the contract gained and this package has not is a failure naming
// the type rather than one naming whichever call site first wanted it.
var _ sqldb.Client = (*rdsDatabase)(nil)

// A relational database on RDS.
//
// Two pools rather than one, because a cluster hands out a writer endpoint and
// a reader endpoint and the point of the reader is that a query sent to it does
// not occupy the writer. They are built separately and only when something asks
// for one, so an application that only ever reads opens no connection to the
// writer.
type rdsDatabase struct {
	databases *databases
	ref       resources.Ref

	settings   sync.Once
	connection connection
	settingErr error

	writer pool
	reader pool
}

type pool struct {
	once sync.Once
	db   *sql.DB
	err  error

	// opened is the pool once it has been built, for a shutdown to read.
	//
	// The same pointer as db, held separately because db is written inside the
	// Once and read by whoever ran it, whereas a shutdown reads from another
	// goroutine entirely and must not consume the Once to find out whether
	// there is anything to close.
	opened atomic.Pointer[sql.DB]
}

// Writer returns a connection to the endpoint that accepts writes.
func (d *rdsDatabase) Writer(ctx context.Context) (sqldb.Conn, error) {
	return d.conn(ctx, &d.writer, false)
}

// Reader returns a connection to the read replica where the deployment recorded
// one, and to the writer where it did not.
//
// Falling back rather than failing, because whether a cluster has a replica is
// a property of the deployment and not of the query: a handler asking to read
// should not have to know which environment it is in.
func (d *rdsDatabase) Reader(ctx context.Context) (sqldb.Conn, error) {
	return d.conn(ctx, &d.reader, true)
}

// conn takes one connection out of a pool.
//
// A *sql.Conn rather than the pool itself, so that Close returns the connection
// rather than shutting down the pool. A handler closing what it was given is
// the ordinary thing to do, and it must not cost the next invocation its
// connections.
func (d *rdsDatabase) conn(ctx context.Context, p *pool, read bool) (sqldb.Conn, error) {
	db, err := d.open(ctx, p, read)
	if err != nil {
		return nil, err
	}
	c, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("celerity: connecting to %s: %w", d.ref, err)
	}
	return &sqlConn{Conn: c}, nil
}

func (d *rdsDatabase) open(ctx context.Context, p *pool, read bool) (*sql.DB, error) {
	settings, err := d.connectionSettings(ctx)
	if err != nil {
		return nil, err
	}

	p.once.Do(func() {
		p.db, p.err = d.build(ctx, settings, read)
		p.opened.Store(p.db)
	})
	return p.db, p.err
}

func (d *rdsDatabase) build(
	ctx context.Context, settings connection, read bool,
) (*sql.DB, error) {
	name, err := driverFor(settings.engine)
	if err != nil {
		return nil, fmt.Errorf("celerity: connecting to %s: %w", d.ref, err)
	}

	host := settings.host
	if read && settings.readHost != "" {
		host = settings.readHost
	}

	db, err := d.openWith(ctx, name, settings, host)
	if err != nil {
		return nil, fmt.Errorf("celerity: connecting to %s: %w", d.ref, err)
	}

	db.SetMaxOpenConns(settings.pool.max)
	db.SetMaxIdleConns(settings.pool.idle)
	db.SetConnMaxIdleTime(settings.pool.idleFor)
	// A database behind a proxy or a failover drops connections that look
	// healthy from here, and a pool that never retires one keeps handing out a
	// connection that fails on first use.
	db.SetConnMaxLifetime(settings.pool.lifetime)
	return db, nil
}

// openWith builds the pool, either from a fixed connection string or from one
// signed per connection.
//
// IAM authentication issues a token that lasts fifteen minutes, which is shorter
// than an execution environment lives and far shorter than a pooled connection
// might. A fixed connection string would work until the token lapsed and then
// fail to open anything, so the string is built again for every connection the
// pool makes.
func (d *rdsDatabase) openWith(
	ctx context.Context, name string, settings connection, host string,
) (*sql.DB, error) {
	if settings.authMode != resources.AuthIAM {
		return sql.Open(name, settings.dsn(host, settings.password))
	}

	base, err := driverOf(name)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(&signingConnector{
		driver: base,
		build: func(ctx context.Context) (string, error) {
			token, err := d.token(ctx, settings, host)
			if err != nil {
				return "", err
			}
			return settings.dsn(host, token), nil
		},
	}), nil
}

func (d *rdsDatabase) token(
	ctx context.Context, settings connection, host string,
) (string, error) {
	cfg, err := d.databases.session.Config(ctx)
	if err != nil {
		return "", err
	}
	region := settings.region
	if region == "" {
		region = cfg.Region
	}

	endpoint := net.JoinHostPort(host, strconv.Itoa(settings.port))
	token, err := auth.BuildAuthToken(ctx, endpoint, region, settings.user, cfg.Credentials)
	if err != nil {
		return "", fmt.Errorf("signing a connection token for %s: %w", d.ref, err)
	}
	return token, nil
}

// close gives back both pools, draining what is in flight.
//
// The pools rather than the handle: a handle is held for the life of the process
// and closing it is not something a handler does, which is why there is no Close
// on the contract and this is reached through the provider instead.
func (d *rdsDatabase) close(ctx context.Context) error {
	var errs []error
	for _, p := range []*pool{&d.writer, &d.reader} {
		if db := p.opened.Load(); db != nil {
			errs = append(errs, closePool(ctx, db, d.ref))
		}
	}
	return errors.Join(errs...)
}

// closePool waits for the connections in use to be given back, up to whatever
// the caller allowed, and then closes the pool anyway.
//
// sql.DB.Close does not wait: it stops new connections and closes the idle
// ones, leaving a query in flight to close its connection when it finishes. The
// wait is what makes a shutdown drain rather than cut.
func closePool(ctx context.Context, db *sql.DB, ref resources.Ref) error {
	for db.Stats().InUse > 0 {
		select {
		case <-ctx.Done():
			if err := db.Close(); err != nil {
				return fmt.Errorf("celerity: closing the pool for %s: %w", ref, err)
			}
			return fmt.Errorf("celerity: %s still had queries running at shutdown", ref)
		case <-time.After(drainPollInterval):
		}
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("celerity: closing the pool for %s: %w", ref, err)
	}
	return nil
}

// drainPollInterval is how often a shutdown looks again at whether the queries
// in flight have finished. Short enough that an idle pool closes at once, long
// enough not to spin.
const drainPollInterval = 20 * time.Millisecond

// sqlConn adapts database/sql to the narrow interface a handler is given, which
// exists so that a driver is never forced on a caller.
type sqlConn struct {
	*sql.Conn
}

func (c *sqlConn) ExecContext(
	ctx context.Context, query string, args ...any,
) (sqldb.Result, error) {
	return c.Conn.ExecContext(ctx, query, args...)
}

func (c *sqlConn) QueryContext(
	ctx context.Context, query string, args ...any,
) (sqldb.Rows, error) {
	return c.Conn.QueryContext(ctx, query, args...)
}

func (c *sqlConn) QueryRowContext(
	ctx context.Context, query string, args ...any,
) sqldb.Row {
	return c.Conn.QueryRowContext(ctx, query, args...)
}

func (c *sqlConn) PrepareContext(ctx context.Context, query string) (sqldb.Stmt, error) {
	stmt, err := c.Conn.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &sqlStmt{Stmt: stmt}, nil
}

func (c *sqlConn) BeginTx(ctx context.Context, opts ...sqldb.TxOption) (sqldb.Tx, error) {
	options := sqldb.ResolveTxOptions(opts)
	tx, err := c.Conn.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.IsolationLevel(options.Isolation),
		ReadOnly:  options.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	return &sqlTx{Tx: tx}, nil
}

// sqlTx adapts a database/sql transaction the same way sqlConn adapts a
// connection, so that Result and Rows stay this SDK's own.
type sqlTx struct {
	*sql.Tx
}

func (t *sqlTx) ExecContext(
	ctx context.Context, query string, args ...any,
) (sqldb.Result, error) {
	return t.Tx.ExecContext(ctx, query, args...)
}

func (t *sqlTx) QueryContext(
	ctx context.Context, query string, args ...any,
) (sqldb.Rows, error) {
	return t.Tx.QueryContext(ctx, query, args...)
}

func (t *sqlTx) QueryRowContext(
	ctx context.Context, query string, args ...any,
) sqldb.Row {
	return t.Tx.QueryRowContext(ctx, query, args...)
}

func (t *sqlTx) PrepareContext(ctx context.Context, query string) (sqldb.Stmt, error) {
	stmt, err := t.Tx.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &sqlStmt{Stmt: stmt}, nil
}

// sqlStmt adapts a database/sql statement the same way sqlConn adapts a
// connection, so that Result, Rows and Row stay this SDK's own.
type sqlStmt struct {
	*sql.Stmt
}

func (s *sqlStmt) ExecContext(ctx context.Context, args ...any) (sqldb.Result, error) {
	return s.Stmt.ExecContext(ctx, args...)
}

func (s *sqlStmt) QueryContext(ctx context.Context, args ...any) (sqldb.Rows, error) {
	return s.Stmt.QueryContext(ctx, args...)
}

func (s *sqlStmt) QueryRowContext(ctx context.Context, args ...any) sqldb.Row {
	return s.Stmt.QueryRowContext(ctx, args...)
}

// signingConnector builds a connection string for every connection the pool
// opens, rather than once for the pool.
type signingConnector struct {
	driver driver.Driver
	build  func(context.Context) (string, error)
}

func (c *signingConnector) Connect(ctx context.Context) (driver.Conn, error) {
	dsn, err := c.build(ctx)
	if err != nil {
		return nil, err
	}
	if withContext, ok := c.driver.(driver.DriverContext); ok {
		connector, err := withContext.OpenConnector(dsn)
		if err != nil {
			return nil, err
		}
		return connector.Connect(ctx)
	}
	return c.driver.Open(dsn)
}

func (c *signingConnector) Driver() driver.Driver {
	return c.driver
}

// driverOf recovers the registered driver behind a name, which is what building
// a connector needs and what database/sql does not otherwise hand out.
func driverOf(name string) (driver.Driver, error) {
	db, err := sql.Open(name, "")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Driver(), nil
}

// The driver names each engine is registered under, in the order they are
// preferred. More than one package can serve an engine, and which of them an
// application chose is the application's business.
var driverNames = map[string][]string{
	sqldb.EnginePostgres: {"pgx", "postgres"},
	sqldb.EngineMySQL:    {"mysql"},
}

// driverFor picks the registered driver for an engine.
//
// A driver is not linked by this package. database/sql takes one from whatever
// the program imported, and compiling every supported driver into every
// application that touches AWS would be a cost paid by applications with no
// database at all.
//
// Which one to import is a deployment fact rather than a decision for handler
// code, so the build writes it: celerity-go generate reads the engine off the
// blueprint's database resources, the same way it reads the deploy target. A
// missing driver therefore means the build did not know about the database, and
// the error says so as well as naming the import that would fix it by hand.
func driverFor(engine string) (string, error) {
	candidates, ok := driverNames[engine]
	if !ok {
		return "", fmt.Errorf(
			"the deployment recorded the engine %q, and Celerity supports %q and %q",
			engine, sqldb.EnginePostgres, sqldb.EngineMySQL)
	}

	registered := sql.Drivers()
	for _, name := range candidates {
		if slices.Contains(registered, name) {
			return name, nil
		}
	}
	return "", fmt.Errorf(
		"no %s driver is linked into this application.\n\n"+
			"The build normally links one: celerity-go generate takes --sql-engine %s from "+
			"the blueprint, so a database the blueprint declares is a driver in the binary. "+
			"Reaching one it does not declare, or building outside the Celerity CLI, needs "+
			"the import written by hand:\n\n\t%s",
		engine, engine, importFor(engine))
}

func importFor(engine string) string {
	if engine == sqldb.EngineMySQL {
		return `_ "github.com/go-sql-driver/mysql"`
	}
	return `_ "github.com/jackc/pgx/v5/stdlib"`
}

// connection is everything the deployment recorded about how to reach a
// database.
type connection struct {
	host     string
	readHost string
	port     int
	user     string
	database string
	engine   string
	authMode string
	password string
	ssl      bool
	region   string
	pool     poolSettings
}

type poolSettings struct {
	max      int
	idle     int
	idleFor  time.Duration
	lifetime time.Duration
}

// dsn builds the connection string for the engine, with whatever is standing in
// as the password.
func (c connection) dsn(host, password string) string {
	address := net.JoinHostPort(host, strconv.Itoa(c.port))

	if c.engine == sqldb.EngineMySQL {
		tls := "false"
		if c.ssl {
			tls = "true"
		}
		return fmt.Sprintf("%s:%s@tcp(%s)/%s?tls=%s&parseTime=true",
			c.user, url.QueryEscape(password), address, c.database, tls)
	}

	mode := "disable"
	if c.ssl {
		// Encrypted without demanding that the server's certificate chain to a
		// root this process trusts. An RDS certificate is signed by Amazon's
		// own authority, which a function's trust store does not necessarily
		// carry, and refusing to connect over that is a harder failure than the
		// one it prevents.
		mode = "require"
	}
	endpoint := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.user, password),
		Host:     address,
		Path:     "/" + c.database,
		RawQuery: url.Values{"sslmode": {mode}}.Encode(),
	}
	return endpoint.String()
}
