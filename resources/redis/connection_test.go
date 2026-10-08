package redis_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/redis"
)

// What an application building its own client is given, which is how the
// capabilities the portable contract leaves out are reached.
type ConnectionTestSuite struct {
	suite.Suite
}

func TestConnectionTestSuite(t *testing.T) {
	suite.Run(t, new(ConnectionTestSuite))
}

func cacheRef(values map[string]string) resources.Ref {
	return refFor(resources.KindCache, "ordersCache", values)
}

func (s *ConnectionTestSuite) connectionOf(
	creds redis.Credentials, values map[string]string,
) cache.Connection {
	client, err := redis.New(cacheRef(values), creds)
	s.Require().NoError(err)
	connection, err := client.Connection(ctx())
	s.Require().NoError(err)
	return connection
}

func (s *ConnectionTestSuite) Test_the_details_are_the_ones_the_deployment_recorded() {
	connection := s.connectionOf(&fakeCredentials{}, map[string]string{
		"ordersCache_host":        "orders.abc.cache.amazonaws.com",
		"ordersCache_port":        "6380",
		"ordersCache_clusterMode": "true",
		"ordersCache_user":        "orders-app",
		"ordersCache_keyPrefix":   "orders:",
	})

	s.Equal("orders.abc.cache.amazonaws.com", connection.Host)
	s.Equal(6380, connection.Port)
	s.True(connection.ClusterMode)
	s.Equal("orders-app", connection.User)
	s.Equal("orders:", connection.KeyPrefix)
	s.Equal(cache.AuthPassword, connection.AuthMode)
}

func (s *ConnectionTestSuite) Test_a_client_built_by_hand_is_told_the_key_prefix() {
	// Nothing else will apply it: the SDK's own operations apply it for
	// themselves, and a client built from these details does not.
	connection := s.connectionOf(&fakeCredentials{}, map[string]string{
		"ordersCache_host":      "127.0.0.1",
		"ordersCache_keyPrefix": "session:",
	})

	s.Equal("session:", connection.KeyPrefix)
}

func (s *ConnectionTestSuite) Test_a_recorded_password_is_handed_over() {
	connection := s.connectionOf(&fakeCredentials{}, map[string]string{
		"ordersCache_host":      "127.0.0.1",
		"ordersCache_authToken": "written-down",
	})

	password, err := connection.Password(ctx())

	s.Require().NoError(err)
	s.Equal("written-down", password)
}

func (s *ConnectionTestSuite) Test_a_password_held_by_reference_is_read_from_the_platform() {
	secret := &recordedSecret{value: "from-the-secret-store"}

	connection := s.connectionOf(&fakeCredentials{secret: secret}, map[string]string{
		"ordersCache_host":              "127.0.0.1",
		"ordersCache_authTokenSecretId": "orders/cache/token",
	})
	password, err := connection.Password(ctx())

	s.Require().NoError(err)
	s.Equal("from-the-secret-store", password)
	s.Equal("orders/cache/token", secret.asked)
}

func (s *ConnectionTestSuite) Test_a_cache_with_no_auth_hands_over_an_empty_password() {
	// Which is what a development session's Valkey is: there is nothing there
	// to protect, and an absent password is not a failure.
	connection := s.connectionOf(nil, map[string]string{
		"ordersCache_host": "127.0.0.1",
		"ordersCache_tls":  "false",
	})

	password, err := connection.Password(ctx())

	s.Require().NoError(err)
	s.Empty(password)
	s.False(connection.TLS)
}

func (s *ConnectionTestSuite) Test_an_identity_signed_password_is_minted_per_call() {
	// A signed token is shorter-lived than the execution environment holding a
	// pool, so this is a function rather than a value, every connection a
	// hand-built client opens signs a fresh one.
	var signings int
	creds := &fakeCredentials{
		signed: func(context.Context) (string, string, error) {
			signings++
			return "orders-app", fmt.Sprintf("token-%d", signings), nil
		},
	}

	connection := s.connectionOf(creds, map[string]string{
		"ordersCache_host":     "orders.abc.cache.amazonaws.com",
		"ordersCache_authMode": resources.AuthIAM,
		"ordersCache_user":     "orders-app",
	})

	s.Zero(signings, "nothing is signed until a connection asks for a password")

	first, err := connection.Password(ctx())
	s.Require().NoError(err)
	second, err := connection.Password(ctx())
	s.Require().NoError(err)

	s.Equal("token-1", first)
	s.Equal("token-2", second, "each connection signs its own")
}

func (s *ConnectionTestSuite) Test_iam_forces_an_encrypted_connection() {
	// A signed token is a credential in its own right, so it travels over
	// something nobody can read whatever the deployment recorded.
	connection := s.connectionOf(&fakeCredentials{}, map[string]string{
		"ordersCache_host":     "orders.abc.cache.amazonaws.com",
		"ordersCache_authMode": resources.AuthIAM,
		"ordersCache_user":     "orders-app",
		"ordersCache_tls":      "false",
	})

	s.True(connection.TLS)
	s.Equal(cache.AuthIAM, connection.AuthMode)
}

func (s *ConnectionTestSuite) Test_iam_without_a_user_says_what_is_missing() {
	// The token is signed for a particular user, so there is nothing to sign
	// without one, and the connection would be refused with an error that says
	// nothing about the user.
	client, err := redis.New(cacheRef(map[string]string{
		"ordersCache_host":     "orders.abc.cache.amazonaws.com",
		"ordersCache_authMode": resources.AuthIAM,
	}), &fakeCredentials{})
	s.Require().NoError(err)

	_, err = client.Connection(ctx())

	s.Require().Error(err)
	s.Contains(err.Error(), "_user")
}

func (s *ConnectionTestSuite) Test_a_cache_with_no_endpoint_recorded_says_so() {
	client, err := redis.New(cacheRef(map[string]string{}), &fakeCredentials{})
	s.Require().NoError(err)

	_, err = client.Connection(ctx())

	s.Require().Error(err)
}

func (s *ConnectionTestSuite) Test_two_caches_are_two_connections() {
	// Two linked caches are two managed caches: two endpoints, two sets of
	// credentials and, where one is a cluster and the other is not, two kinds
	// of client. A handle is built per resource, so nothing is shared between
	// them but the package that reaches them.
	sessions, err := redis.New(refFor(resources.KindCache, "sessionCache", map[string]string{
		"sessionCache_host": "sessions.abc.cache.amazonaws.com",
	}), &fakeCredentials{})
	s.Require().NoError(err)

	rates, err := redis.New(refFor(resources.KindCache, "rateCache", map[string]string{
		"rateCache_host":        "rates.xyz.cache.amazonaws.com",
		"rateCache_clusterMode": "true",
	}), &fakeCredentials{})
	s.Require().NoError(err)

	sessionsConn, err := sessions.Connection(ctx())
	s.Require().NoError(err)
	ratesConn, err := rates.Connection(ctx())
	s.Require().NoError(err)

	s.Equal("sessions.abc.cache.amazonaws.com", sessionsConn.Host)
	s.Equal("rates.xyz.cache.amazonaws.com", ratesConn.Host)
	s.False(sessionsConn.ClusterMode)
	s.True(ratesConn.ClusterMode, "the cluster flag decides which kind of client is built")
}
