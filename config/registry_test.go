package config_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// Reading a platform's own store is an import rather than something an
// application wires up, the way a serverless adapter and a resource provider
// are, so that core doesn't import cloud SDKs.
//
// One provider per platform, registered once for the life of the test binary,
// because that is what an init does and there is no unregistering. The
// platforms are shared out between the cases below, and a case cannot invent
// one: an unrecognised name reads as PlatformOther.
type RegistryTestSuite struct {
	suite.Suite
}

func TestRegistryTestSuite(t *testing.T) {
	suite.Run(t, new(RegistryTestSuite))
}

// The kind this provider holds configuration in, which is its own vocabulary
// and is carried through from the deployment without core interpreting it.
const servedKind = config.StoreKind("one-big-secret")

// storeProvider serves one kind of store and refuses the rest.
type storeProvider struct{}

func (storeProvider) Name() string              { return "fake-store" }
func (storeProvider) Platform() config.Platform { return config.PlatformGCP }

func (storeProvider) Backend(kind config.StoreKind) (config.Backend, error) {
	if kind != servedKind {
		return nil, errors.New("this platform does not hold configuration that way")
	}
	return config.MapBackend{
		"orders-config": {"REGION": "europe-west2"},
	}, nil
}

// cachingProvider's backend holds values between reads for itself, which is
// what a platform's own caching layer does. On AWS whether that applies depends
// on the function running behind the parameters and secrets extension, which is
// a thing the provider knows and core cannot.
type cachingProvider struct{ backend *cachingBackend }

func (cachingProvider) Name() string              { return "fake-caching" }
func (cachingProvider) Platform() config.Platform { return config.PlatformAzure }
func (p cachingProvider) Backend(config.StoreKind) (config.Backend, error) {
	return p.backend, nil
}

type cachingBackend struct{ fetches int }

func (*cachingBackend) Caching() bool { return true }

func (b *cachingBackend) Fetch(context.Context, string) (map[string]string, error) {
	b.fetches++
	return map[string]string{"REGION": "eu-west-2"}, nil
}

var cachedBackend = &cachingBackend{}

func init() {
	config.RegisterProvider(storeProvider{})
	config.RegisterProvider(cachingProvider{backend: cachedBackend})
}

func (s *RegistryTestSuite) Test_the_linked_providers_are_reported() {
	s.Contains(config.RegisteredProviders(), "fake-store")
	s.Contains(config.RegisteredProviders(), "fake-caching")
}

func (s *RegistryTestSuite) Test_a_linked_provider_is_what_a_store_is_read_through() {
	s.T().Setenv(config.StoreIDEnvVar, "orders-config")
	s.T().Setenv(config.StoreKindEnvVar, string(servedKind))
	s.T().Setenv(config.PlatformEnvVar, "gcp")

	svc, err := config.FromEnvironment()
	s.Require().NoError(err)

	value, err := svc.Get(context.Background(), "REGION")

	s.Require().NoError(err)
	s.Equal("europe-west2", value)
}

func (s *RegistryTestSuite) Test_a_store_kind_the_platform_cannot_hold_is_reported() {
	// Rather than a silently empty store: it means the deployment asked for
	// something this provider cannot do.
	s.T().Setenv(config.StoreIDEnvVar, "orders-config")
	s.T().Setenv(config.StoreKindEnvVar, "a-kind-it-does-not-serve")
	s.T().Setenv(config.PlatformEnvVar, "gcp")

	_, err := config.FromEnvironment()

	s.Require().Error(err)
	s.Contains(err.Error(), "fake-store")
	s.Contains(err.Error(), "a-kind-it-does-not-serve",
		"the error should name the kind the deployment asked for")
}

func (s *RegistryTestSuite) Test_a_backend_that_caches_for_itself_is_not_refreshed_on_top_of() {
	// Refreshing a cache costs a request to re-read a cache rather than the
	// store, so the backend says it caches and this package leaves it alone.
	// Core does not decide it, because deciding it needs to know which
	// platforms cache and when.
	s.T().Setenv(config.StoreIDEnvVar, "orders-config")
	s.T().Setenv(config.PlatformEnvVar, "azure")
	s.T().Setenv(config.RefreshEnvVar, "1")

	svc, err := config.FromEnvironment()
	s.Require().NoError(err)

	before := cachedBackend.fetches
	for range 3 {
		_, err := svc.Get(context.Background(), "REGION")
		s.Require().NoError(err)
		time.Sleep(10 * time.Millisecond)
	}

	s.Equal(1, cachedBackend.fetches-before,
		"a refresh interval of 1ms was applied over a backend that caches already")
}

func (s *RegistryTestSuite) Test_two_providers_for_one_platform_is_a_mistake_worth_a_panic() {
	// It can only mean two providers claiming one platform, which nothing can
	// resolve at runtime.
	config.RegisterProvider(storeOther{})

	s.Panics(func() { config.RegisterProvider(storeOther{}) })
}

type storeOther struct{}

func (storeOther) Name() string              { return "fake-other" }
func (storeOther) Platform() config.Platform { return config.PlatformOther }
func (storeOther) Backend(config.StoreKind) (config.Backend, error) {
	return config.EmptyBackend{}, nil
}
