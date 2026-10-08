package service_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
)

// How the tracing middleware reaches every AWS client.
type SessionTracingTestSuite struct {
	suite.Suite
}

func TestSessionTracingTestSuite(t *testing.T) {
	suite.Run(t, new(SessionTracingTestSuite))
}

// loaded is a session over a configuration that loads without reaching AWS.
func (s *SessionTracingTestSuite) loaded(region string) *service.Session {
	return service.NewSessionWith(func(context.Context) (aws.Config, error) {
		return aws.Config{Region: region}, nil
	})
}

func (s *SessionTracingTestSuite) Test_the_configuration_every_client_is_built_from_traces() {
	// One addition covers every service, because what it is added to is the
	// configuration rather than a client: S3, SQS, SNS, DynamoDB and Secrets
	// Manager are all built from the one this process loaded.
	cfg, err := s.loaded("eu-west-1").Config(awstest.Ctx())

	s.Require().NoError(err)
	s.Len(cfg.APIOptions, 1, "the tracing middleware, which is not optional")
}

func (s *SessionTracingTestSuite) Test_it_is_added_once_however_many_times_config_is_asked_for() {
	// The configuration is loaded once and shared, so a second caller must not
	// get a second copy of the middleware on it.
	session := s.loaded("eu-west-1")

	first, err := session.Config(awstest.Ctx())
	s.Require().NoError(err)
	second, err := session.Config(awstest.Ctx())
	s.Require().NoError(err)

	s.Len(first.APIOptions, 1)
	s.Len(second.APIOptions, 1)
}

func (s *SessionTracingTestSuite) Test_a_configuration_that_did_not_load_is_not_handed_back() {
	// Nothing to trace, and a zero configuration handed back would build
	// clients that reach nothing with middleware on them.
	session := service.NewSessionWith(func(context.Context) (aws.Config, error) {
		return aws.Config{}, awstest.ErrNoCredentials
	})

	cfg, err := session.Config(awstest.Ctx())

	s.Require().Error(err)
	s.Empty(cfg.APIOptions)
	s.Empty(cfg.Region)
}
