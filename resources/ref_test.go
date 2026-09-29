package resources_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

type RefTestSuite struct {
	suite.Suite
}

func TestRefTestSuite(t *testing.T) {
	suite.Run(t, new(RefTestSuite))
}

var errNoCredentials = errors.New("no credentials")

type failingBackend struct{}

func (failingBackend) Fetch(context.Context, string) (map[string]string, error) {
	return nil, errNoCredentials
}

// Builds the two things a deployment writes, the topology, saying
// which key holds a resource, and the identifiers themselves.
func serviceWith(links config.Links, values map[string]string) *config.Service {
	svc := config.New().WithLinks(links)
	svc.Register(config.ResourcesNamespace, config.NewNamespace(
		config.MapBackend{"resources": values}, "resources"))
	return svc
}

func (s *RefTestSuite) Test_an_identifier_is_the_name_the_deployment_created_it_under() {
	ref := resources.Ref{
		Kind: resources.KindBucket,
		Name: "ordersBucket",
		Config: serviceWith(
			config.Links{"ordersBucket": {Type: "bucket", ConfigKey: "ordersBucketName"}},
			map[string]string{"ordersBucketName": "orders-bucket-prod-eu-west-2"},
		),
	}

	id, err := ref.ID(context.Background())

	s.Require().NoError(err)
	s.Equal("orders-bucket-prod-eu-west-2", id)
}

func (s *RefTestSuite) Test_a_resource_asked_for_as_the_wrong_kind_is_refused() {
	// Resolving it would hand a queue's URL to something about to treat it as
	// a bucket, which fails somewhere much less obvious than here.
	ref := resources.Ref{
		Kind: resources.KindBucket,
		Name: "ordersQueue",
		Config: serviceWith(
			config.Links{"ordersQueue": {Type: "queue", ConfigKey: "ordersQueueUrl"}},
			map[string]string{"ordersQueueUrl": "https://sqs.eu-west-2.amazonaws.com/1/orders"},
		),
	}

	_, err := ref.ID(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "is a queue")
	s.Contains(err.Error(), "asked for as a bucket")
}

func (s *RefTestSuite) Test_a_resource_the_topology_does_not_hold_names_the_ones_it_does() {
	ref := resources.Ref{
		Kind: resources.KindBucket,
		Name: "invoicesBucket",
		Config: serviceWith(
			config.Links{"ordersBucket": {Type: "bucket", ConfigKey: "ordersBucketName"}},
			map[string]string{},
		),
	}

	_, err := ref.ID(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "invoicesBucket")
	s.Contains(err.Error(), "ordersBucket", "the error should say what is declared")
}

func (s *RefTestSuite) Test_the_several_values_recorded_about_one_resource_are_read_by_suffix() {
	// A cache or a database is not one identifier, and each value the
	// deployment recorded sits under the resource's own key.
	ref := resources.Ref{
		Kind: resources.KindSQLDatabase,
		Name: "ordersDb",
		Config: serviceWith(
			config.Links{"ordersDb": {Type: "sqlDatabase", ConfigKey: "ordersDb"}},
			map[string]string{
				"ordersDb_host": "orders.cluster-abc.eu-west-2.rds.amazonaws.com",
				"ordersDb_port": "5432",
			},
		),
	}

	host, ok, err := ref.Field(context.Background(), "_host")
	s.Require().NoError(err)
	s.True(ok)
	s.Equal("orders.cluster-abc.eu-west-2.rds.amazonaws.com", host)

	_, ok, err = ref.Field(context.Background(), "_readHost")
	s.Require().NoError(err)
	s.False(ok, "a value the deployment did not record is absent, not an error")
}

func (s *RefTestSuite) Test_an_application_with_no_configuration_says_so() {
	// Rather than dereferencing nothing. This is a binary built without the
	// deployment's own wiring, and the message has to say that is what happened.
	ref := resources.Ref{Kind: resources.KindBucket, Name: "ordersBucket"}

	_, idErr := ref.ID(context.Background())
	_, _, fieldErr := ref.Field(context.Background(), "_host")

	for _, err := range []error{idErr, fieldErr} {
		s.Require().Error(err)
		s.Contains(err.Error(), `bucket "ordersBucket"`)
		s.Contains(err.Error(), "no configuration")
	}
}
