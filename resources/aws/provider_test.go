package aws_test

import (
	"testing"

	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/bucket"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/cache"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/datastore"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/queue"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/sqldb"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/topic"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsresources "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
)

// What the provider is for: an application names a resource the way its
// blueprint does, and everything about what AWS calls it, and which service it
// lives in, is decided here.
type ProviderTestSuite struct {
	suite.Suite
}

func TestProviderTestSuite(t *testing.T) {
	suite.Run(t, new(ProviderTestSuite))
}

func (s *ProviderTestSuite) Test_the_provider_serves_every_resource_a_blueprint_can_declare() {
	// A kind the provider cannot build would be a handle that is nil at the
	// call site, reported nowhere near the blueprint that declared it.
	provider := awsresources.New()
	ref := resources.Ref{Name: "orders"}

	bucket, err := provider.Bucket(ref)
	s.Require().NoError(err)
	s.NotNil(bucket)

	queue, err := provider.Queue(ref)
	s.Require().NoError(err)
	s.NotNil(queue)

	topic, err := provider.Topic(ref)
	s.Require().NoError(err)
	s.NotNil(topic)

	datastore, err := provider.Datastore(ref)
	s.Require().NoError(err)
	s.NotNil(datastore)

	cache, err := provider.Cache(ref)
	s.Require().NoError(err)
	s.NotNil(cache)

	database, err := provider.SQLDatabase(ref)
	s.Require().NoError(err)
	s.NotNil(database)
}

func (s *ProviderTestSuite) Test_the_provider_is_registered_for_aws() {
	s.Contains(resources.RegisteredProviders(), "aws",
		"importing the package should be all an application does to select it")
}

func (s *ProviderTestSuite) Test_the_platform_decides_between_linked_providers() {
	cases := []struct {
		platform string
		detected bool
	}{
		{"aws", true},
		{"gcp", false},
		{"local", false},
		{"", false},
	}

	for _, tc := range cases {
		s.Run("platform "+tc.platform, func() {
			s.T().Setenv(config.PlatformEnvVar, tc.platform)
			s.Equal(tc.detected, awsresources.New().Detect())
		})
	}
}
