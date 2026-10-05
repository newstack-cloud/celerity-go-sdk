package cache_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awscache "github.com/newstack-cloud/celerity-go-sdk/resources/aws/cache"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"

	"github.com/stretchr/testify/suite"
)

// Both of these mint a short-lived credential from the function's own, and
// neither is a request to a service that could answer wrongly and be noticed.
// A malformed token is a connection refused, which says nothing about what was
// wrong with it, so what goes into one is worth stating here.
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

const cacheHost = "orders.abc.cache.amazonaws.com"

func (s *IAMTestSuite) Test_a_cache_token_is_a_signed_request_to_connect() {
	tokens := awscache.TokensFor(s.credentialled(), cacheHost, "orders-app", "eu-west-2")

	user, token, err := tokens.Credentials(context.Background())

	s.Require().NoError(err)
	s.Equal("orders-app", user, "the connection authenticates as the user it signed for")

	// What ElastiCache takes is the signed URL with its scheme removed, which
	// is not a form the signer produces.
	s.False(strings.HasPrefix(token, "https://"), "the scheme should have been stripped")
	s.True(strings.HasPrefix(token, cacheHost+"/?"), "got %q", token)

	query := s.queryOf(token)
	s.Equal("connect", query.Get("Action"))
	s.Equal("orders-app", query.Get("User"))
	s.Equal("900", query.Get("X-Amz-Expires"),
		"a presigned request carrying no lifetime is refused")
	s.NotEmpty(query.Get("X-Amz-Signature"))
	s.Contains(query.Get("X-Amz-Credential"), "eu-west-2/elasticache/aws4_request",
		"signed for the service and region the cluster is in")
}

func (s *IAMTestSuite) Test_a_cache_token_is_held_until_it_is_nearly_spent() {
	// Signing reads the credentials rather than making a request, so it is
	// cheap but not free, and a pool asks on every connection it opens.
	tokens := awscache.TokensFor(s.credentialled(), cacheHost, "orders-app", "eu-west-2")
	ctx := context.Background()

	_, first, err := tokens.Credentials(ctx)
	s.Require().NoError(err)
	_, second, err := tokens.Credentials(ctx)
	s.Require().NoError(err)
	s.Equal(first, second)

	tokens.Expire()
	spent := tokens.ExpiresAt()
	_, third, err := tokens.Credentials(ctx)
	s.Require().NoError(err)
	// Signed within the same second, so the token itself is byte-identical:
	// what says it was signed again is that the lifetime moved.
	s.Equal(first, third)
	s.True(tokens.ExpiresAt().After(spent), "a spent token should have been signed again")
}

func (s *IAMTestSuite) Test_a_cache_token_falls_back_to_the_region_the_function_runs_in() {
	// The deployment records one only where the cluster is somewhere else.
	tokens := awscache.TokensFor(s.credentialled(), cacheHost, "orders-app", "")

	_, token, err := tokens.Credentials(context.Background())

	s.Require().NoError(err)
	s.Contains(s.queryOf(token).Get("X-Amz-Credential"), "eu-west-2/elasticache")
}

func (s *IAMTestSuite) Test_a_cache_token_cannot_be_signed_without_a_region() {
	s.T().Setenv("AWS_ACCESS_KEY_ID", "AKIAEXAMPLE")
	s.T().Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	s.T().Setenv("AWS_REGION", "")
	s.T().Setenv("AWS_DEFAULT_REGION", "")

	_, _, err := awscache.TokensFor(service.NewSession(), cacheHost, "orders-app", "").
		Credentials(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "region")
}

// queryOf reads the query a signed token carries, which is where the parameters
// that make it a presigned request live.
func (s *IAMTestSuite) queryOf(token string) url.Values {
	_, raw, found := strings.Cut(token, "?")
	s.Require().True(found, "the token should carry a query: %q", token)
	query, err := url.ParseQuery(raw)
	s.Require().NoError(err)
	return query
}

// A password the deployment recorded by reference is read from Secrets Manager,
// which is the other half of what AWS contributes to a cache.
func (s *IAMTestSuite) Test_a_recorded_password_is_read_from_the_secret_it_lives_in() {
	secrets := &awstest.Secrets{Value: "the-generated-token"}
	session := service.NewSession()
	session.UseSecrets(secrets)

	password, err := awscache.Credentials(session, awstest.SimpleRef(
		resources.KindCache, "ordersCache", "orders-prod")).
		Secret(context.Background(), "orders/cache/token")

	s.Require().NoError(err)
	s.Equal("the-generated-token", password)
	s.Equal("orders/cache/token", secrets.Asked)
}

func (s *IAMTestSuite) Test_a_secret_that_cannot_be_read_says_so() {
	secrets := &awstest.Secrets{Err: awstest.ErrNoCredentials}
	session := service.NewSession()
	session.UseSecrets(secrets)

	_, err := awscache.Credentials(session, awstest.SimpleRef(
		resources.KindCache, "ordersCache", "orders-prod")).
		Secret(context.Background(), "orders/cache/token")

	s.Require().Error(err)
}
