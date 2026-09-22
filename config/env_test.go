package config_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// The platform is the one thing this package reads from the environment
// directly, because it is what decides which provider a store is read through.
// Everything else comes from a store.
type EnvTestSuite struct {
	suite.Suite
}

func TestEnvTestSuite(t *testing.T) {
	suite.Run(t, new(EnvTestSuite))
}

func (s *EnvTestSuite) Test_the_platform_is_read_from_the_environment() {
	cases := []struct {
		env  string
		want config.Platform
	}{
		{"aws", config.PlatformAWS},
		{"AWS", config.PlatformAWS},
		{"azure", config.PlatformAzure},
		{"gcp", config.PlatformGCP},
		{"local", config.PlatformLocal},
		// Used to choose a default, and a platform nobody named has no default
		// to choose, so it is not an error.
		{"", config.PlatformOther},
		{"something-new", config.PlatformOther},
	}

	for _, tc := range cases {
		s.Run(tc.env, func() {
			s.T().Setenv(config.PlatformEnvVar, tc.env)

			s.Equal(tc.want, config.CurrentPlatform())
		})
	}
}
