package sqldb

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
)

// The ports each engine listens on where the deployment recorded none.
const (
	defaultPostgresPort = 5432
	defaultMySQLPort    = 3306
)

// Reads what the deployment recorded about the database,
// once.
//
// Reading it involves a config store and possibly a secret, so it is done on
// the first call a handler makes and held for the life of the process. A
// rotated password is picked up when the execution environment is recycled,
// which is the same bargain the config package makes for a store read without a
// refresh interval.
func (d *rdsDatabase) connectionSettings(ctx context.Context) (connection, error) {
	d.settings.Do(func() {
		d.connection, d.settingErr = d.readSettings(ctx)
	})
	return d.connection, d.settingErr
}

func (d *rdsDatabase) readSettings(ctx context.Context) (connection, error) {
	f := resources.Fields{Ref: d.ref}

	c, err := d.readRecorded(ctx, f)
	if err != nil {
		return connection{}, err
	}
	if c.pool, err = d.readPool(ctx, f); err != nil {
		return connection{}, err
	}
	if c.password, err = d.readPassword(ctx, f, c.authMode); err != nil {
		return connection{}, err
	}
	return c, nil
}

// The values the deployment wrote under the resource's own key.
func (d *rdsDatabase) readRecorded(ctx context.Context, f resources.Fields) (connection, error) {
	r := f.Reader()
	var c connection

	c.host = r.Required(ctx, "_host")
	c.user = r.Required(ctx, "_user")
	c.engine = r.Optional(ctx, "_engine", sqldb.EnginePostgres)
	// After the engine, whose port it falls back to.
	c.port = r.Number(ctx, "_port", defaultPortFor(c.engine))
	// The blueprint's own name for the resource, where the deployment recorded
	// no other, which is what a single-database cluster is created with.
	c.database = r.Optional(ctx, "_database", d.ref.Name)
	c.readHost = r.Optional(ctx, "_readHost", "")
	c.authMode = r.Optional(ctx, "_authMode", resources.AuthPassword)
	c.region = r.Optional(ctx, "_region", "")
	c.ssl = r.Boolean(ctx, "_ssl", true)
	if err := r.Err(); err != nil {
		return connection{}, err
	}

	// IAM authentication is a signed token, which is a credential in its own
	// right and must not cross the network in the clear.
	if c.authMode == resources.AuthIAM {
		c.ssl = true
	}
	return c, nil
}

// The password a connection authenticates with.
//
// Empty where the database is reached with the platform's own identity, whose
// credential is a token signed per connection rather than anything the
// deployment recorded.
func (d *rdsDatabase) readPassword(
	ctx context.Context, f resources.Fields, authMode string,
) (string, error) {
	if authMode == resources.AuthIAM {
		return "", nil
	}

	password, ok, err := service.Secret(ctx, d.databases.session, f, "_password", "_credentialsSecretId")
	if err != nil {
		return "", err
	}

	if !ok {
		return "", fmt.Errorf(
			"celerity: %s is reached with a password, and the deployment recorded neither "+
				"%q nor %q", d.ref, "_password", "_credentialsSecretId")
	}

	return service.PasswordIn(password, d.ref)
}

func defaultPortFor(engine string) int {
	if engine == sqldb.EngineMySQL {
		return defaultMySQLPort
	}
	return defaultPostgresPort
}

// The pool a function wants, which is not the pool a long-lived process wants.
//
// A single function process serves one invocation at a time, so more than a couple of
// connections is a couple of connections held open against a cluster's
// connection limit for nothing, and an idle one should be given up quickly
// because the environment is frozen between invocations and the connection is
// dead long before the database notices.
var lambdaPool = poolSettings{
	max: 2, idle: 1, idleFor: time.Second, lifetime: 5 * time.Minute,
}

// A containerised deployment serves concurrently and stays up, so connections
// are worth keeping.
var runtimePool = poolSettings{
	max: 10, idle: 2, idleFor: 30 * time.Second, lifetime: 30 * time.Minute,
}

// LambdaFunctionEnvVar is set by AWS in every Lambda execution environment.
//
// It is AWS's own variable and this is the AWS module, which is the only reason
// it can be read here: core holds no provider's environment variables.
const LambdaFunctionEnvVar = "AWS_LAMBDA_FUNCTION_NAME"

func (d *rdsDatabase) readPool(ctx context.Context, f resources.Fields) (poolSettings, error) {
	settings := runtimePool
	if os.Getenv(LambdaFunctionEnvVar) != "" {
		settings = lambdaPool
	}

	r := f.Reader()
	max := r.Number(ctx, "_poolMax", settings.max)
	idle := r.Number(ctx, "_poolMin", settings.idle)
	idleFor := r.Number(ctx, "_poolIdleTimeoutMs", int(settings.idleFor.Milliseconds()))
	if err := r.Err(); err != nil {
		return poolSettings{}, err
	}

	settings.max = max
	settings.idle = idle
	settings.idleFor = time.Duration(idleFor) * time.Millisecond
	return settings, nil
}
