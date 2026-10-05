package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
)

// A resource a deployment created for this application is in the application's
// own region, which is what every handle shared one client for. A resource the
// blueprint declares as external may be anywhere, and a service routes a
// request by the region the client was built for.
type ClientsTestSuite struct {
	suite.Suite
}

func TestClientsTestSuite(t *testing.T) {
	suite.Run(t, new(ClientsTestSuite))
}

func ambient(region string) *service.Session {
	return service.NewSessionWith(func(context.Context) (aws.Config, error) {
		return aws.Config{Region: region}, nil
	})
}

func (s *ClientsTestSuite) Test_an_unrecorded_region_is_the_applications_own() {
	cfg, err := ambient("eu-west-1").ConfigFor(awstest.Ctx(), "")

	s.Require().NoError(err)
	s.Equal("eu-west-1", cfg.Region)
}

func (s *ClientsTestSuite) Test_a_recorded_region_replaces_the_applications_own() {
	session := ambient("eu-west-1")

	elsewhere, err := session.ConfigFor(awstest.Ctx(), "us-east-1")
	s.Require().NoError(err)
	s.Equal("us-east-1", elsewhere.Region)

	own, err := session.ConfigFor(awstest.Ctx(), "")
	s.Require().NoError(err)
	s.Equal("eu-west-1", own.Region,
		"the ambient configuration is not mutated by asking for another region")
}

func (s *ClientsTestSuite) Test_a_failure_to_load_the_configuration_is_reported() {
	session := service.NewSessionWith(func(context.Context) (aws.Config, error) {
		return aws.Config{}, awstest.ErrNoCredentials
	})

	_, err := session.ConfigFor(awstest.Ctx(), "us-east-1")

	s.Require().Error(err)
	s.ErrorIs(err, awstest.ErrNoCredentials)
}

func (s *ClientsTestSuite) Test_a_client_is_built_once_per_key_and_shared_after() {
	var clients service.Clients[string]
	built := 0

	for range 3 {
		got, err := clients.Get(awstest.Ctx(), service.ClientKey{Region: "eu-west-1"},
			func() (string, error) { built++; return "first", nil })
		s.Require().NoError(err)
		s.Equal("first", got)
	}

	s.Equal(1, built, "a client is expensive enough to build once")
}

func (s *ClientsTestSuite) Test_two_regions_are_two_clients() {
	var clients service.Clients[string]

	own, err := clients.Get(awstest.Ctx(), service.ClientKey{Region: "eu-west-1"},
		func() (string, error) { return "eu-west-1", nil })
	s.Require().NoError(err)

	elsewhere, err := clients.Get(awstest.Ctx(), service.ClientKey{Region: "us-east-1"},
		func() (string, error) { return "us-east-1", nil })
	s.Require().NoError(err)

	s.Equal("eu-west-1", own)
	s.Equal("us-east-1", elsewhere,
		"a second region gets its own client rather than the first one's")
}

// What every later caller for a key gets is what the first call produced,
// including a failure: the usual cause is credentials the process does not have.
func (s *ClientsTestSuite) Test_a_failure_to_build_is_what_every_later_caller_for_that_key_gets() {
	var clients service.Clients[string]
	failed := errors.New("no credentials")
	built := 0

	for range 2 {
		_, err := clients.Get(awstest.Ctx(), service.ClientKey{},
			func() (string, error) { built++; return "", failed })
		s.ErrorIs(err, failed)
	}

	s.Equal(1, built)
}

func (s *ClientsTestSuite) Test_concurrent_callers_for_one_key_build_one_client() {
	var clients service.Clients[string]
	var mu sync.Mutex
	built := 0

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = clients.Get(awstest.Ctx(), service.ClientKey{Region: "eu-west-1"},
				func() (string, error) {
					mu.Lock()
					built++
					mu.Unlock()
					return "one", nil
				})
		}()
	}
	wg.Wait()

	s.Equal(1, built)
}

func (s *ClientsTestSuite) Test_the_region_is_read_from_what_the_deployment_recorded() {
	cases := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{
			"a resource this deployment created records none",
			map[string]string{"ordersBucket": "orders-prod"},
			"",
		},
		{
			"an external resource records its own",
			map[string]string{"ordersBucket": "acme-archive", "ordersBucket_region": "us-east-1"},
			"us-east-1",
		},
	}

	for _, test := range cases {
		s.Run(test.name, func() {
			ref := awstest.Ref(resources.KindBucket, "ordersBucket", test.values)

			key, err := service.KeyFor(awstest.Ctx(), ref)

			s.Require().NoError(err)
			s.Equal(test.want, key.Region)
		})
	}
}
