package sqldb

import (
	"context"
	"database/sql/driver"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
)

// Databases returns a builder driving the given session, so that a suite
// exercises the real client against a stand-in of what a credential is read
// from.
func Databases(s *service.Session) func(resources.Ref) (databaseClient, error) {
	return (&databases{session: s}).build
}

// Connection is what a database made of the values its deployment recorded.
type Connection = connection

// ReadSettings resolves those values, which is otherwise reachable only by
// opening a connection to a database a unit test has none of.
func ReadSettings(
	ctx context.Context, s *service.Session, ref resources.Ref,
) (Connection, error) {
	return (&rdsDatabase{databases: &databases{session: s}, ref: ref}).readSettings(ctx)
}

// DSN builds the connection string an engine takes. A wrong one is invisible
// until a connection is refused, which is a long way from the mistake.
func (c Connection) DSN(host, password string) string {
	return c.dsn(host, password)
}

// Host, ReadHost, Pool and AuthMode report what was resolved, so a test can
// check the settings without reaching into the package's own fields.
func (c Connection) Host() string {
	return c.host
}
func (c Connection) ReadHost() string {
	return c.readHost
}
func (c Connection) AuthMode() string {
	return c.authMode
}
func (c Connection) Password() string {
	return c.password
}
func (c Connection) SSL() bool {
	return c.ssl
}
func (c Connection) MaxConns() int {
	return c.pool.max
}

// DriverFor is the engine-to-driver choice, which decides an error a developer
// is expected to act on.
func DriverFor(engine string) (string, error) {
	return driverFor(engine)
}

// Token signs the token a database reached with IAM authentication uses as its
// password.
func Token(
	ctx context.Context, s *service.Session, ref resources.Ref, host string,
) (string, error) {
	d := &rdsDatabase{databases: &databases{session: s}, ref: ref}
	settings, err := d.readSettings(ctx)
	if err != nil {
		return "", err
	}
	return d.token(ctx, settings, host)
}

// SigningConnector builds a connection string for every connection a pool
// opens, which is what a token that outlives nothing can do and a fixed string
// cannot.
func SigningConnector(
	base driver.Driver, build func(context.Context) (string, error),
) driver.Connector {
	return &signingConnector{driver: base, build: build}
}

// DriverOf recovers the registered driver behind a name, which building a
// connector needs and database/sql does not otherwise hand out.
func DriverOf(name string) (driver.Driver, error) {
	return driverOf(name)
}
