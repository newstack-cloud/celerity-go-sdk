//go:build integration

package local_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/config/local"
)

// What the Celerity CLI writes into a local session's Valkey: one key to a
// store, holding a JSON object of the values. Read against a real Valkey rather
// than a fake, because what is being checked is agreement with the CLI about a
// format, and a fake would only agree with this package.
//
// Run by scripts/run-tests.sh --with-integration, which starts the Valkey they
// need. Skipped where there is none, so a run of this module on its own
// still passes.
type LocalIntegrationTestSuite struct {
	suite.Suite
	client *redis.Client
}

func TestLocalIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(LocalIntegrationTestSuite))
}

func (s *LocalIntegrationTestSuite) SetupSuite() {
	addr := os.Getenv("CELERITY_TEST_VALKEY_ADDR")
	if addr == "" {
		port := os.Getenv(local.PortEnvVar)
		if port == "" {
			port = "6379"
		}
		addr = "127.0.0.1:" + port
	}

	s.client = redis.NewClient(&redis.Options{Addr: addr})
	if err := s.client.Ping(context.Background()).Err(); err != nil {
		s.T().Skipf("no valkey at %s: %v", addr, err)
	}
}

// seed writes a store the way the CLI does.
func (s *LocalIntegrationTestSuite) seed(storeID string, values map[string]any) {
	encoded, err := json.Marshal(values)
	s.Require().NoError(err)
	s.Require().NoError(s.client.Set(context.Background(), storeID, string(encoded), 0).Err())
	s.T().Cleanup(func() { s.client.Del(context.Background(), storeID) })
}

// backend is the provider's backend, reached the way the config package
// reaches it.
func (s *LocalIntegrationTestSuite) backend() config.Backend {
	backend, err := (&local.Provider{}).Backend("")
	s.Require().NoError(err)
	return backend
}

func (s *LocalIntegrationTestSuite) Test_a_store_is_read_as_the_cli_wrote_it() {
	s.seed("ordersConfig", map[string]any{"REGION": "eu-west-2", "RETRIES": "3"})

	values, err := s.backend().Fetch(context.Background(), "ordersConfig")

	s.Require().NoError(err)
	s.Equal(map[string]string{"REGION": "eu-west-2", "RETRIES": "3"}, values)
}

func (s *LocalIntegrationTestSuite) Test_values_a_blueprint_gave_as_themselves_arrive_as_text() {
	// A store is seeded from a blueprint, and YAML gives numbers and booleans
	// as themselves. They reach a handler as the text they would have been read
	// as from any other store, so what a value means does not depend on where
	// it was held.
	s.seed("ordersConfig", map[string]any{
		"RETRIES": 3,
		"RATIO":   0.25,
		"DEBUG":   true,
		"ABSENT":  nil,
		"NESTED":  map[string]any{"a": 1},
	})

	values, err := s.backend().Fetch(context.Background(), "ordersConfig")

	s.Require().NoError(err)
	s.Equal("3", values["RETRIES"])
	s.Equal("0.25", values["RATIO"])
	s.Equal("true", values["DEBUG"])
	s.Empty(values["ABSENT"])
	s.JSONEq(`{"a":1}`, values["NESTED"])
}

func (s *LocalIntegrationTestSuite) Test_a_store_nothing_has_written_to_is_empty() {
	// A local session seeds what the blueprint declares, so a key that is not
	// there is a store with nothing in it rather than a store that is missing.
	values, err := s.backend().Fetch(context.Background(), "neverSeeded")

	s.Require().NoError(err)
	s.Empty(values)
}

func (s *LocalIntegrationTestSuite) Test_a_store_that_does_not_hold_an_object_is_refused() {
	s.Require().NoError(
		s.client.Set(context.Background(), "brokenConfig", "not json", 0).Err())
	s.T().Cleanup(func() { s.client.Del(context.Background(), "brokenConfig") })

	_, err := s.backend().Fetch(context.Background(), "brokenConfig")

	s.Require().Error(err)
	s.Contains(err.Error(), "JSON object")
}

func (s *LocalIntegrationTestSuite) Test_the_whole_service_is_built_from_what_a_session_sets() {
	// End to end: the variables a session sets, the store it seeded, and a
	// value read off the service the way a handler reads one.
	s.seed("ordersConfig", map[string]any{"REGION": "eu-west-2"})

	s.T().Setenv("CELERITY_CONFIG_ORDERSCONFIG_STORE_ID", "ordersConfig")
	s.T().Setenv("CELERITY_CONFIG_ORDERSCONFIG_NAMESPACE", "ordersConfig")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()
	s.Require().NoError(err)

	ns, err := svc.Namespace("ordersConfig")
	s.Require().NoError(err)

	value, err := ns.Get(context.Background(), "REGION")
	s.Require().NoError(err)
	s.Equal("eu-west-2", value)
}
