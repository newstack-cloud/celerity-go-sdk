package aws_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	awsconfig "github.com/newstack-cloud/celerity-go-sdk/config/aws"
)

// Which AWS store a value is read from is the deployment's decision, made from
// the blueprint's plaintext list and stated in the environment. These cases
// pin down that each kind reaches the right service, and that the extension is
// used only where it can serve.
type ProviderTestSuite struct {
	suite.Suite
}

func TestProviderTestSuite(t *testing.T) {
	suite.Run(t, new(ProviderTestSuite))
}

func (s *ProviderTestSuite) Test_importing_the_package_registers_it_for_aws() {
	s.Contains(config.RegisteredProviders(), awsconfig.Name)
}

func (s *ProviderTestSuite) Test_a_store_of_parameters_is_read_from_parameter_store() {
	var asked string
	provider := awsconfig.ProviderWith(nil, func(path string, _ *string) (map[string]string, *string, error) {
		asked = path
		return map[string]string{"/orders/REGION": "eu-west-2"}, nil, nil
	})

	backend, err := provider.Backend(awsconfig.StoreParameterStore)
	s.Require().NoError(err)

	values, err := backend.Fetch(context.Background(), "/orders")

	s.Require().NoError(err)
	s.Equal("/orders/", asked, "the path is read as a prefix, with the separator it needs")
	s.Equal(map[string]string{"REGION": "eu-west-2"}, values,
		"a parameter is keyed by what it is called within the store")
}

func (s *ProviderTestSuite) Test_a_store_held_as_a_secret_is_read_from_secrets_manager() {
	var asked string
	provider := awsconfig.ProviderWith(func(secretID string) (*string, error) {
		asked = secretID
		value := `{"REGION":"eu-west-2","RETRIES":3}`
		return &value, nil
	}, nil)

	backend, err := provider.Backend(awsconfig.StoreSecretsManager)
	s.Require().NoError(err)

	values, err := backend.Fetch(context.Background(), "orders-config")

	s.Require().NoError(err)
	s.Equal("orders-config", asked)
	// A number the blueprint gave as itself reads as the text it would from
	// any other store.
	s.Equal(map[string]string{"REGION": "eu-west-2", "RETRIES": "3"}, values)
}

func (s *ProviderTestSuite) Test_a_deployment_that_said_nothing_gets_a_secret() {
	// The default, since a store commonly holds values that are all sensitive.
	provider := awsconfig.ProviderWith(func(string) (*string, error) {
		value := `{"REGION":"eu-west-2"}`
		return &value, nil
	}, nil)

	backend, err := provider.Backend("")
	s.Require().NoError(err)

	values, err := backend.Fetch(context.Background(), "orders-config")

	s.Require().NoError(err)
	s.Equal("eu-west-2", values["REGION"])
}

func (s *ProviderTestSuite) Test_a_kind_aws_does_not_hold_configuration_in_is_refused() {
	// Rather than every value reading as absent: it means the deployment asked
	// for something this provider cannot do.
	_, err := awsconfig.ProviderWith(nil, nil).Backend("app-configuration")

	s.Require().Error(err)
	s.Contains(err.Error(), "app-configuration")
	s.Contains(err.Error(), string(awsconfig.StoreSecretsManager),
		"the error should name what AWS does hold configuration in")
	s.Contains(err.Error(), string(awsconfig.StoreParameterStore))
}

func (s *ProviderTestSuite) Test_a_secret_with_nothing_in_it_yet_is_an_empty_store() {
	// The deployment created it and has not written to it, which is a store
	// with nothing in it rather than one that is missing.
	cases := []struct {
		name  string
		value *string
	}{
		{"no string value at all", nil},
		{"an empty string value", func() *string { v := ""; return &v }()},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			provider := awsconfig.ProviderWith(
				func(string) (*string, error) { return tc.value, nil }, nil)
			backend, err := provider.Backend(awsconfig.StoreSecretsManager)
			s.Require().NoError(err)

			values, err := backend.Fetch(context.Background(), "orders-config")

			s.Require().NoError(err)
			s.Empty(values)
		})
	}
}

func (s *ProviderTestSuite) Test_a_store_that_could_not_be_read_names_the_service() {
	provider := awsconfig.ProviderWith(
		func(string) (*string, error) { return nil, errors.New("AccessDeniedException") }, nil)
	backend, err := provider.Backend(awsconfig.StoreSecretsManager)
	s.Require().NoError(err)

	_, err = backend.Fetch(context.Background(), "orders-config")

	s.Require().Error(err)
	s.Contains(err.Error(), "Secrets Manager")
	s.Contains(err.Error(), "orders-config")
	s.Contains(err.Error(), "AccessDeniedException")
}

func (s *ProviderTestSuite) Test_a_store_larger_than_one_page_is_read_whole() {
	// Read as whatever fitted in the first page, a configuration is subtly
	// wrong rather than failing, which is the worse outcome of the two.
	pages := 0
	provider := awsconfig.ProviderWith(nil, func(_ string, next *string) (map[string]string, *string, error) {
		pages++
		if next == nil {
			token := "page-2"
			return map[string]string{"/orders/A": "1"}, &token, nil
		}
		return map[string]string{"/orders/B": "2"}, nil, nil
	})

	backend, err := provider.Backend(awsconfig.StoreParameterStore)
	s.Require().NoError(err)

	values, err := backend.Fetch(context.Background(), "/orders")

	s.Require().NoError(err)
	s.Equal(2, pages)
	s.Equal(map[string]string{"A": "1", "B": "2"}, values)
}

func (s *ProviderTestSuite) Test_a_page_that_could_not_be_read_stops_the_read() {
	provider := awsconfig.ProviderWith(nil, func(string, *string) (map[string]string, *string, error) {
		return nil, nil, errors.New("ThrottlingException")
	})
	backend, err := provider.Backend(awsconfig.StoreParameterStore)
	s.Require().NoError(err)

	_, err = backend.Fetch(context.Background(), "/orders")

	s.Require().Error(err)
	s.Contains(err.Error(), "Parameter Store")
	s.Contains(err.Error(), "ThrottlingException")
}
