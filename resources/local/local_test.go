package local_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/local"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// What this covers is the division of labour: which resource kinds a session
// serves itself and which it hands to the platform's provider. What actually
// serves them is a backend package, covered against a real Valkey, since a
// stand-in for it would only agree with whatever it was told.
type LocalTestSuite struct {
	suite.Suite
}

func TestLocalTestSuite(t *testing.T) {
	suite.Run(t, new(LocalTestSuite))
}

// This stands in for the deploy target's provider, which is what a
// session leaves everything but a queue and a topic to.
type platformProvider struct {
	asked []resources.Kind
}

func (p *platformProvider) Name() string { return "aws" }

func (p *platformProvider) Detect() bool {
	return config.CurrentPlatform() == config.PlatformAWS
}

func (p *platformProvider) Bucket(resources.Ref) (bucket.Store, error) {
	p.asked = append(p.asked, resources.KindBucket)
	return nil, nil
}

func (p *platformProvider) Cache(resources.Ref) (cache.Client, error) {
	p.asked = append(p.asked, resources.KindCache)
	return nil, nil
}

func (p *platformProvider) Datastore(resources.Ref) (datastore.Client, error) {
	p.asked = append(p.asked, resources.KindDatastore)
	return nil, nil
}

func (p *platformProvider) SQLDatabase(resources.Ref) (sqldb.Client, error) {
	p.asked = append(p.asked, resources.KindSQLDatabase)
	return nil, nil
}

func (p *platformProvider) Queue(resources.Ref) (queue.Client, error) {
	p.asked = append(p.asked, resources.KindQueue)
	return nil, nil
}

func (p *platformProvider) Topic(resources.Ref) (topic.Client, error) {
	p.asked = append(p.asked, resources.KindTopic)
	return nil, nil
}

func (s *LocalTestSuite) Test_exactly_one_provider_detects_a_session() {
	// A session's deploy target is still the platform the application will be
	// deployed to, so both providers are linked and exactly one of them has to
	// detect, whatever order the generated file imports them in.
	s.T().Setenv(config.PlatformEnvVar, string(config.PlatformLocal))

	s.True(local.New().Detect())
	s.False((&platformProvider{}).Detect())
}

func (s *LocalTestSuite) Test_exactly_one_provider_detects_a_deployment() {
	s.T().Setenv(config.PlatformEnvVar, string(config.PlatformAWS))

	s.False(local.New().Detect())
	s.True((&platformProvider{}).Detect())
}

func (s *LocalTestSuite) Test_everything_but_a_queue_and_a_topic_goes_to_the_platform() {
	// A session's bucket and data store are stand-ins that speak the real
	// protocols, so they are reached the way they always were.
	platform := &platformProvider{}
	provider := local.WithPlatform(platform)
	ref := resources.Ref{Name: "ordersBucket"}

	_, err := provider.Bucket(ref)
	s.Require().NoError(err)
	_, err = provider.Cache(ref)
	s.Require().NoError(err)
	_, err = provider.Datastore(ref)
	s.Require().NoError(err)
	_, err = provider.SQLDatabase(ref)
	s.Require().NoError(err)

	s.Equal([]resources.Kind{
		resources.KindBucket,
		resources.KindCache,
		resources.KindDatastore,
		resources.KindSQLDatabase,
	}, platform.asked)
}

func (s *LocalTestSuite) Test_a_queue_and_a_topic_are_not_delegated() {
	// No backend is linked in this test binary, so the two kinds a session
	// serves itself report what the build left out rather than reaching the
	// platform's provider.
	platform := &platformProvider{}
	provider := local.WithPlatform(platform)
	ref := resources.Ref{Name: "ordersQueue"}

	_, queueErr := provider.Queue(ref)
	_, topicErr := provider.Topic(ref)

	s.Require().Error(queueErr)
	s.Require().Error(topicErr)
	s.Contains(queueErr.Error(), "no backend linked")
	s.Contains(queueErr.Error(), "resources/local/redis",
		"the error should name the import a build left out")
	s.Empty(platform.asked, "neither should have been delegated")
}

func (s *LocalTestSuite) Test_a_session_without_a_platform_to_delegate_to_says_so() {
	// A build that linked this and nothing else, which the generated file does
	// not produce but a hand-written blank import could. New rather than
	// WithPlatform, so the fallback is the registry, which holds only what this
	// package's own init put there.
	provider := local.New()

	_, err := provider.Bucket(resources.Ref{Name: "ordersBucket"})

	s.Require().Error(err)
	s.Contains(err.Error(), "ordersBucket")
	s.Contains(err.Error(), "none is linked")
}

func (s *LocalTestSuite) Test_closing_a_session_with_no_backend_linked_does_nothing() {
	// A backend registers what it holds, so a build that linked none has
	// nothing to give back.
	provider := local.New()

	s.Require().NoError(provider.Close(context.Background()))
}
