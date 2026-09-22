package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// An application shouldn't need to do anything to get its configuration, the deploy engine
// describes the stores in the environment, because the identifier cannot come
// from the blueprint, a celerity/config resource that omits its name having one
// generated when it is created.
type DiscoverTestSuite struct {
	suite.Suite
}

func TestDiscoverTestSuite(t *testing.T) {
	suite.Run(t, new(DiscoverTestSuite))
}

func (s *DiscoverTestSuite) Test_one_store_is_read_without_being_named() {
	s.T().Setenv(config.StoreIDEnvVar, "orders-config")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	// Registered as the default, so it is read straight off the service.
	s.Equal([]string{config.DefaultNamespace}, svc.Registered())
}

func (s *DiscoverTestSuite) Test_several_stores_are_each_named() {
	s.T().Setenv("CELERITY_CONFIG_ORDERS_STORE_ID", "orders-config")
	s.T().Setenv("CELERITY_CONFIG_BILLING_STORE_ID", "billing-config")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	s.Equal([]string{"billing", "orders"}, svc.Registered(),
		"a name that was not given is the key, lowercased")
}

func (s *DiscoverTestSuite) Test_the_name_a_handler_asks_for_can_be_stated() {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "stated outright",
			env:  map[string]string{"CELERITY_CONFIG_ORDERS_NAMESPACE": "ordersConfig"},
			want: "ordersConfig",
		},
		{
			// A deployment that says only where the values sit within the
			// store still produces a namespace with a usable name.
			name: "taken from the prefix",
			env:  map[string]string{"CELERITY_CONFIG_ORDERS_STORE_PREFIX": "orders"},
			want: "orders",
		},
		{
			name: "the name it was described under",
			want: "orders",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.T().Setenv("CELERITY_CONFIG_ORDERS_STORE_ID", "orders-config")
			s.T().Setenv(config.PlatformEnvVar, "local")
			for name, value := range tc.env {
				s.T().Setenv(name, value)
			}

			svc, err := config.FromEnvironment()

			s.Require().NoError(err)
			s.Equal([]string{tc.want}, svc.Registered())
		})
	}
}

func (s *DiscoverTestSuite) Test_the_unnamed_store_is_not_read_as_one_called_store() {
	// CELERITY_CONFIG_STORE_ID ends in _STORE_ID like every named one, so
	// reading it the same way would register a namespace called "store".
	s.T().Setenv(config.StoreIDEnvVar, "orders-config")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	s.Equal([]string{config.DefaultNamespace}, svc.Registered())
	s.NotContains(svc.Registered(), "store")
}

func (s *DiscoverTestSuite) Test_a_prefix_is_carried_onto_the_namespace() {
	// What the prefix does to the values is covered against a backend in the
	// namespace suite; here it is that the deployment's prefix reaches it.
	s.T().Setenv("CELERITY_CONFIG_ORDERS_STORE_ID", "shared-config")
	s.T().Setenv("CELERITY_CONFIG_ORDERS_STORE_PREFIX", "orders")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	s.Equal([]string{"orders"}, svc.Registered())
}

func (s *DiscoverTestSuite) Test_a_deployment_that_described_no_store() {
	// Not an error: a failure at startup over configuration nothing may read
	// is worse than being told, where a value is asked for, that the
	// application declares none.
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	s.Empty(svc.Registered())

	_, err = svc.Get(context.Background(), "REGION")
	s.Require().Error(err)
	s.Contains(err.Error(), "celerity/config")
}

func (s *DiscoverTestSuite) Test_a_platform_with_no_provider_linked_reads_empty() {
	// The application asked for configuration this binary carries no way to
	// read. Every value is absent rather than the process failing to start.
	//
	// A local run with no local provider linked is the same case, this package
	// does not invent a store of its own, because the one the Celerity CLI
	// populates has to be the same one for every SDK.
	for _, platform := range []string{"aws", "local"} {
		s.Run(platform, func() {
			s.T().Setenv(config.StoreIDEnvVar, "orders-config")
			s.T().Setenv(config.PlatformEnvVar, platform)

			svc, err := config.FromEnvironment()
			s.Require().NoError(err)

			_, ok, err := svc.Lookup(context.Background(), "REGION")
			s.Require().NoError(err)
			s.False(ok)
		})
	}
}

// The ordinary shape where the resources store the deploy engine always describes,
// and one the blueprint declared for the application itself.
func (s *DiscoverTestSuite) Test_the_resources_store_sits_alongside_the_applications_own() {
	s.T().Setenv("CELERITY_CONFIG_RESOURCES_STORE_ID", "resources")
	s.T().Setenv("CELERITY_CONFIG_ORDERSCONFIG_STORE_ID", "ordersConfig")
	s.T().Setenv("CELERITY_CONFIG_ORDERSCONFIG_NAMESPACE", "ordersConfig")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	// Sorted, so the application's own store comes first by name rather than
	// by having been described second.
	s.Equal([]string{"ordersConfig", config.ResourcesNamespace}, svc.Registered())
}

func (s *DiscoverTestSuite) Test_an_unnamed_store_does_not_displace_the_resources_one() {
	// Read on its own, the unnamed form would be the only store discovered,
	// and every resource handle would then have no identifier to resolve.
	s.T().Setenv(config.StoreIDEnvVar, "ordersConfig")
	s.T().Setenv("CELERITY_CONFIG_RESOURCES_STORE_ID", "resources")
	s.T().Setenv(config.PlatformEnvVar, "local")

	svc, err := config.FromEnvironment()

	s.Require().NoError(err)
	s.Contains(svc.Registered(), config.ResourcesNamespace)
	s.Contains(svc.Registered(), config.DefaultNamespace)
}

func (s *DiscoverTestSuite) Test_the_applications_own_store_is_read_without_being_named() {
	// Two stores, and no ambiguity: the resources one is the deployment's
	// rather than the application's, so it does not count towards the choice.
	s.T().Setenv(config.StoreIDEnvVar, "ordersConfig")
	s.T().Setenv("CELERITY_CONFIG_RESOURCES_STORE_ID", "resources")
	s.T().Setenv(config.PlatformEnvVar, "registered-for-this-suite")

	svc, err := config.FromEnvironment()
	s.Require().NoError(err)

	// Reading through the namespace directly, since this suite links no
	// provider and the values themselves are covered elsewhere.
	ns, err := svc.Namespace(config.DefaultNamespace)
	s.Require().NoError(err)
	s.Require().NotNil(ns)

	resources, err := svc.Namespace(config.ResourcesNamespace)
	s.Require().NoError(err)
	s.Require().NotNil(resources)
}
