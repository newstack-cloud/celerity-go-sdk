package sqldb_test

import (
	"database/sql"
	"database/sql/driver"
	"slices"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awssqldb "github.com/newstack-cloud/celerity-go-sdk/resources/aws/sqldb"
)

type SQLDatabaseTestSuite struct {
	suite.Suite
}

func TestSQLDatabaseTestSuite(t *testing.T) {
	suite.Run(t, new(SQLDatabaseTestSuite))
}

func databaseRef(values map[string]string) resources.Ref {
	return awstest.Ref(resources.KindSQLDatabase, "ordersDb", values)
}

// recorded is the ordinary deployment: an endpoint, a user, and a password in a
// secret.
func recorded(extra map[string]string) map[string]string {
	values := map[string]string{
		"ordersDb_host": "orders.cluster-abc.eu-west-2.rds.amazonaws.com",
		"ordersDb_user": "orders_app",
	}
	for key, value := range extra {
		values[key] = value
	}
	return values
}

func (s *SQLDatabaseTestSuite) Test_the_connection_is_built_from_what_the_deployment_recorded() {
	secrets := &awstest.Secrets{Value: `{"username":"orders_app","password":"s3cret"}`}
	session := service.NewSession()
	session.UseSecrets(secrets)

	settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(map[string]string{
		"ordersDb_readHost":            "orders.cluster-ro-abc.eu-west-2.rds.amazonaws.com",
		"ordersDb_credentialsSecretId": "orders/db/credentials",
	})))

	s.Require().NoError(err)
	s.Equal("orders.cluster-abc.eu-west-2.rds.amazonaws.com", settings.Host())
	s.Equal("orders.cluster-ro-abc.eu-west-2.rds.amazonaws.com", settings.ReadHost())
	s.Equal("s3cret", settings.Password())
	s.True(settings.SSL(), "encrypted unless the deployment says otherwise")
}

func (s *SQLDatabaseTestSuite) Test_a_password_is_taken_from_a_secret_of_either_shape() {
	cases := []struct {
		name   string
		secret string
		want   string
	}{
		{
			// AWS deployments create the secret in this shape to match an
			// AWS-managed RDS secret, so the same rotation tooling works
			// against either.
			name:   "an object of credential fields",
			secret: `{"username":"orders_app","password":"s3cret"}`,
			want:   "s3cret",
		},
		{
			// A secret store holding a plain string, as one holding a
			// generated password commonly does, needs no wrapper.
			name:   "the password itself",
			secret: "s3cret",
			want:   "s3cret",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			secrets := &awstest.Secrets{Value: tc.secret}
			session := service.NewSession()
			session.UseSecrets(secrets)

			settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(
				map[string]string{"ordersDb_credentialsSecretId": "orders/db"})))

			s.Require().NoError(err)
			s.Equal(tc.want, settings.Password())
		})
	}
}

func (s *SQLDatabaseTestSuite) Test_a_secret_that_is_an_object_with_no_password_is_refused() {
	// A secret of the wrong kind, such as one pointed at the cluster's master
	// secret by mistake. Sending the whole object as the password would fail as
	// a wrong password, which is the hardest thing to tell from a rotation that
	// has not finished.
	secrets := &awstest.Secrets{Value: `{"username":"orders_app","engine":"postgres"}`}
	session := service.NewSession()
	session.UseSecrets(secrets)

	_, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(
		map[string]string{"ordersDb_credentialsSecretId": "orders/db"})))

	s.Require().Error(err)
	s.Contains(err.Error(), "no password field")
}

func (s *SQLDatabaseTestSuite) Test_password_authentication_with_no_password_recorded_is_refused() {
	session := service.NewSession()

	_, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(nil)))

	s.Require().Error(err)
	s.Contains(err.Error(), "_password")
	s.Contains(err.Error(), "_credentialsSecretId")
}

func (s *SQLDatabaseTestSuite) Test_iam_authentication_needs_no_password_and_forces_encryption() {
	// The token is a credential in its own right, so it must not cross the
	// network in the clear whatever the deployment recorded.
	session := service.NewSession()

	settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(map[string]string{
		"ordersDb_authMode": "iam",
		"ordersDb_ssl":      "false",
	})))

	s.Require().NoError(err)
	s.Equal("iam", settings.AuthMode())
	s.True(settings.SSL())
	s.Empty(settings.Password())
}

func (s *SQLDatabaseTestSuite) Test_the_database_defaults_to_the_name_the_blueprint_gave_it() {
	// Which is what a single-database cluster is created with.
	session := service.NewSession()

	settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(map[string]string{
		"ordersDb_authMode": "iam",
	})))

	s.Require().NoError(err)
	s.Contains(settings.DSN("host", "pw"), "/ordersDb")
}

func (s *SQLDatabaseTestSuite) Test_a_connection_string_is_built_for_the_engine() {
	cases := []struct {
		name   string
		engine string
		ssl    bool
		want   string
	}{
		{
			name:   "postgres, encrypted",
			engine: "postgres",
			ssl:    true,
			want:   "postgres://orders_app:p%40ss@orders:5432/orders?sslmode=require",
		},
		{
			name:   "postgres, not encrypted",
			engine: "postgres",
			ssl:    false,
			want:   "postgres://orders_app:p%40ss@orders:5432/orders?sslmode=disable",
		},
		{
			name:   "mysql, encrypted",
			engine: "mysql",
			ssl:    true,
			want:   "orders_app:p%40ss@tcp(orders:3306)/orders?tls=true&parseTime=true",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			session := service.NewSession()
			settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(map[string]string{
				"ordersDb_host":     "orders",
				"ordersDb_user":     "orders_app",
				"ordersDb_database": "orders",
				"ordersDb_engine":   tc.engine,
				"ordersDb_password": "unused-here",
				"ordersDb_ssl":      boolText(tc.ssl),
			}))
			s.Require().NoError(err)

			// A password with a character that means something in a URL, which
			// is the case a hand-built string gets wrong.
			s.Equal(tc.want, settings.DSN("orders", "p@ss"))
		})
	}
}

func (s *SQLDatabaseTestSuite) Test_each_engine_listens_on_its_own_port() {
	cases := []struct {
		engine string
		port   string
	}{
		{"postgres", ":5432"},
		{"mysql", ":3306"},
	}

	for _, tc := range cases {
		s.Run(tc.engine, func() {
			session := service.NewSession()
			settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(map[string]string{
				"ordersDb_host":     "orders",
				"ordersDb_user":     "orders_app",
				"ordersDb_engine":   tc.engine,
				"ordersDb_authMode": "iam",
			}))

			s.Require().NoError(err)
			s.Contains(settings.DSN("orders", "pw"), tc.port)
		})
	}
}

func (s *SQLDatabaseTestSuite) Test_a_function_keeps_a_smaller_pool_than_a_long_lived_process() {
	// A function serves one invocation at a time, so more than a couple of
	// connections is a couple held open against a cluster's limit for nothing.
	s.T().Setenv(awssqldb.LambdaFunctionEnvVar, "orders-api")
	session := service.NewSession()

	settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(
		map[string]string{"ordersDb_authMode": "iam"})))

	s.Require().NoError(err)
	s.Equal(2, settings.MaxConns())
}

func (s *SQLDatabaseTestSuite) Test_the_deployment_can_size_the_pool_itself() {
	s.T().Setenv(awssqldb.LambdaFunctionEnvVar, "orders-api")
	session := service.NewSession()

	settings, err := awssqldb.ReadSettings(awstest.Ctx(), session, databaseRef(recorded(map[string]string{
		"ordersDb_authMode": "iam",
		"ordersDb_poolMax":  "8",
	})))

	s.Require().NoError(err)
	s.Equal(8, settings.MaxConns())
}

func (s *SQLDatabaseTestSuite) Test_a_driver_is_taken_from_what_the_application_imported() {
	// The SDK links none: compiling every supported driver into every
	// application that touches AWS is a cost paid by applications with no
	// database at all.
	// Registered here unless something already has, which is the case under
	// the integration build where the suite imports a real pgx. database/sql
	// refuses the same name twice and has no way to give one back.
	if !slices.Contains(sql.Drivers(), "pgx") {
		sql.Register("pgx", stubDriver{})
	}

	name, err := awssqldb.DriverFor("postgres")

	s.Require().NoError(err)
	s.Equal("pgx", name)
}

func (s *SQLDatabaseTestSuite) Test_a_missing_driver_points_at_the_build_that_should_have_linked_it() {
	// The build writes the import from the blueprint, so a missing driver
	// means the build did not know about the database. An error saying only
	// "import a driver" would send a developer to edit source that the build
	// is meant to write.
	if slices.Contains(sql.Drivers(), "mysql") {
		s.T().Skip("a mysql driver is linked, which the integration build does")
	}

	_, err := awssqldb.DriverFor("mysql")

	s.Require().Error(err)
	s.Contains(err.Error(), "--sql-engine mysql")
	s.Contains(err.Error(), "go-sql-driver/mysql")
}

func (s *SQLDatabaseTestSuite) Test_an_engine_celerity_does_not_support_is_refused() {
	_, err := awssqldb.DriverFor("oracle")

	s.Require().Error(err)
	s.Contains(err.Error(), "oracle")
	s.Contains(err.Error(), "postgres")
}

// stubDriver stands in for a driver an application imported. It never connects:
// what is under test is which name was chosen, not what it does.
type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return nil, driver.ErrBadConn
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
