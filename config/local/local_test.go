package local_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/config/local"
)

// What holds without a Valkey to read. Everything that reads one is in the
// integration suite alongside this, which runs against a real Valkey rather
// than a fake: what is being checked there is agreement with the Celerity CLI
// about a format, and a fake would only agree with this package.
type LocalTestSuite struct {
	suite.Suite
}

func TestLocalTestSuite(t *testing.T) {
	suite.Run(t, new(LocalTestSuite))
}

func (s *LocalTestSuite) Test_importing_the_package_registers_it_for_the_local_platform() {
	// Which is what makes a local session read configuration without an
	// application saying anything.
	s.Contains(config.RegisteredProviders(), local.Name)
}

func (s *LocalTestSuite) Test_it_serves_the_local_platform() {
	provider := &local.Provider{}

	s.Equal(config.PlatformLocal, provider.Platform())
	s.Equal(local.Name, provider.Name())
}

func (s *LocalTestSuite) Test_every_store_is_read_the_same_way() {
	// A local session holds every store as one key, because there is nothing
	// here to hold them differently: the kinds a deployment names are a
	// cloud's own services.
	provider := &local.Provider{}

	for _, kind := range []config.StoreKind{"", "secrets-manager", "parameter-store"} {
		backend, err := provider.Backend(kind)

		s.Require().NoError(err, "kind %q", kind)
		s.Require().NotNil(backend)
	}
}
