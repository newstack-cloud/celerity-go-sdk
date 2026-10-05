package sqldb_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	awssqldb "github.com/newstack-cloud/celerity-go-sdk/resources/aws/sqldb"
)

// An IAM connection signs a fresh token per connection, which is what a token
// shorter-lived than the pool that uses it requires.
type IAMTestSuite struct {
	suite.Suite
}

func TestIAMTestSuite(t *testing.T) {
	suite.Run(t, new(IAMTestSuite))
}

// credentialled gives the process something to sign with, which on a deployed
// function is the execution role's own.
func (s *IAMTestSuite) credentialled() *service.Session {
	s.T().Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
	s.T().Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	s.T().Setenv("AWS_REGION", "eu-west-2")
	return service.NewSession()
}

func (s *IAMTestSuite) Test_a_database_token_is_signed_for_the_endpoint_and_the_user() {
	session := s.credentialled()
	ref := awstest.Ref(resources.KindSQLDatabase, "ordersDb", map[string]string{
		"ordersDb_host":     "orders.cluster-abc.eu-west-2.rds.amazonaws.com",
		"ordersDb_user":     "orders_app",
		"ordersDb_authMode": "iam",
	})

	token, err := awssqldb.Token(context.Background(), session, ref,
		"orders.cluster-abc.eu-west-2.rds.amazonaws.com")

	s.Require().NoError(err)
	// RDS takes the token as the password, and it is the endpoint it was signed
	// for: a token for the writer will not open the reader.
	s.Contains(token, "orders.cluster-abc.eu-west-2.rds.amazonaws.com:5432")
	s.Contains(token, "DBUser=orders_app")
	s.Contains(token, "X-Amz-Signature=")
	// Signed for rds-db rather than rds: connecting is its own service in IAM,
	// and a token signed for the control plane is refused.
	s.Contains(token, "eu-west-2%2Frds-db%2Faws4_request")
}

// recordingDriver stands in for the driver an application linked, and keeps
// every connection string it was opened with.
type recordingDriver struct {
	opened []string
}

func (d *recordingDriver) Open(dsn string) (driver.Conn, error) {
	d.opened = append(d.opened, dsn)
	return nil, driver.ErrBadConn
}

func (s *IAMTestSuite) Test_a_connection_string_is_built_again_for_every_connection() {
	// A signed token lasts fifteen minutes, which is shorter than an execution
	// environment lives and far shorter than a pooled connection might. A fixed
	// connection string would work until the token lapsed and then open
	// nothing.
	recorder := &recordingDriver{}
	signed := 0
	connector := awssqldb.SigningConnector(recorder,
		func(context.Context) (string, error) {
			signed++
			return "postgres://orders_app:token-" + string(rune('a'+signed-1)) + "@orders/orders", nil
		})

	ctx := context.Background()
	_, first := connector.Connect(ctx)
	_, second := connector.Connect(ctx)

	s.Require().Error(first)
	s.Require().Error(second)
	s.Equal(2, signed, "each connection should have been given its own token")
	s.Require().Len(recorder.opened, 2)
	s.NotEqual(recorder.opened[0], recorder.opened[1])
	s.Same(recorder, connector.Driver())
}

func (s *IAMTestSuite) Test_a_connection_is_not_opened_when_a_token_cannot_be_signed() {
	// Opening with an empty string would fail as a malformed connection string,
	// which says nothing about the credentials that could not be read.
	recorder := &recordingDriver{}
	connector := awssqldb.SigningConnector(recorder,
		func(context.Context) (string, error) { return "", awstest.ErrNoCredentials })

	_, err := connector.Connect(context.Background())

	s.Require().ErrorIs(err, awstest.ErrNoCredentials)
	s.Empty(recorder.opened)
}
