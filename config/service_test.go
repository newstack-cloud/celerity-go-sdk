package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

type ServiceTestSuite struct {
	suite.Suite
}

func TestServiceTestSuite(t *testing.T) {
	suite.Run(t, new(ServiceTestSuite))
}

func (s *ServiceTestSuite) service(stores config.MapBackend, names ...string) *config.Service {
	svc := config.New()
	for _, name := range names {
		svc.Register(name, config.NewNamespace(stores, name))
	}
	return svc
}

func (s *ServiceTestSuite) Test_one_namespace_is_read_without_being_named() {
	svc := s.service(config.MapBackend{
		"settings": {"REGION": "eu-west-2"},
	}, "settings")

	value, err := svc.Get(context.Background(), "REGION")

	s.Require().NoError(err)
	s.Equal("eu-west-2", value)
}

func (s *ServiceTestSuite) Test_several_namespaces_have_to_be_named() {
	// Reading whichever happened to be registered first would be a value from
	// the wrong store, and the application would run and read something
	// plausible.
	svc := s.service(config.MapBackend{
		"orders":  {"TIMEOUT": "30s"},
		"billing": {"TIMEOUT": "90s"},
	}, "orders", "billing")

	_, err := svc.Get(context.Background(), "TIMEOUT")

	var ambiguous *config.AmbiguousNamespaceError
	s.Require().ErrorAs(err, &ambiguous)
	s.Contains(err.Error(), "orders")
	s.Contains(err.Error(), "billing")

	ns, err := svc.Namespace("billing")
	s.Require().NoError(err)
	value, err := ns.Get(context.Background(), "TIMEOUT")
	s.Require().NoError(err)
	s.Equal("90s", value)
}

func (s *ServiceTestSuite) Test_the_resources_namespace_does_not_count_as_a_choice() {
	// It is populated by the deployment rather than declared by the
	// application, so an application with one config resource still has
	// exactly one to read.
	svc := s.service(config.MapBackend{
		"settings":  {"REGION": "eu-west-2"},
		"resources": {"ORDERS_BUCKET": "orders-prod-eu-west-2"},
	}, "settings", config.ResourcesNamespace)

	value, err := svc.Get(context.Background(), "REGION")

	s.Require().NoError(err)
	s.Equal("eu-west-2", value)
}

func (s *ServiceTestSuite) Test_a_namespace_nothing_registered_names_what_is_registered() {
	svc := s.service(config.MapBackend{"settings": {}}, "settings")

	_, err := svc.Namespace("billing")

	var missing *config.MissingNamespaceError
	s.Require().ErrorAs(err, &missing)
	s.Contains(err.Error(), "settings", "the cause is nearly always a name disagreeing with the blueprint")
}

func (s *ServiceTestSuite) Test_an_application_with_no_config_says_so() {
	svc := config.New()

	_, err := svc.Get(context.Background(), "REGION")

	var missing *config.MissingNamespaceError
	s.Require().ErrorAs(err, &missing)
	s.Contains(err.Error(), "celerity/config")
}

func (s *ServiceTestSuite) Test_a_resource_is_resolved_through_its_link() {
	// Two steps, and both are needed: the links file says which key holds the
	// identifier, and the deployment put the identifier there.
	svc := s.service(config.MapBackend{
		config.ResourcesNamespace: {"ORDERS_BUCKET_NAME": "orders-prod-eu-west-2"},
	}, config.ResourcesNamespace).WithLinks(config.Links{
		"ordersBucket": {Type: "bucket", ConfigKey: "ORDERS_BUCKET_NAME"},
	})

	name, err := svc.Resource(context.Background(), "bucket", "ordersBucket")

	s.Require().NoError(err)
	s.Equal("orders-prod-eu-west-2", name)
}

func (s *ServiceTestSuite) Test_a_resource_asked_for_as_the_wrong_kind_is_refused() {
	// Resolving it would hand back a queue's URL to something about to treat
	// it as a bucket.
	svc := s.service(config.MapBackend{
		config.ResourcesNamespace: {"ORDERS_QUEUE_URL": "https://sqs.eu-west-2.amazonaws.com/1/orders"},
	}, config.ResourcesNamespace).WithLinks(config.Links{
		"orders": {Type: "queue", ConfigKey: "ORDERS_QUEUE_URL"},
	})

	_, err := svc.Resource(context.Background(), "bucket", "orders")

	s.Require().Error(err)
	s.Contains(err.Error(), "is a queue")
	s.Contains(err.Error(), "asked for as a bucket")
}

func (s *ServiceTestSuite) Test_a_resource_the_blueprint_never_declared_lists_what_it_did() {
	svc := s.service(config.MapBackend{
		config.ResourcesNamespace: {},
	}, config.ResourcesNamespace).WithLinks(config.Links{
		"ordersBucket":  {Type: "bucket", ConfigKey: "A"},
		"reportsBucket": {Type: "bucket", ConfigKey: "B"},
		"ordersQueue":   {Type: "queue", ConfigKey: "C"},
	})

	_, err := svc.Resource(context.Background(), "bucket", "invoicesBucket")

	var missing *config.MissingLinkError
	s.Require().ErrorAs(err, &missing)
	s.Contains(err.Error(), "ordersBucket")
	s.Contains(err.Error(), "reportsBucket")
	s.NotContains(err.Error(), "ordersQueue", "only buckets are worth listing for a bucket")
}

func (s *ServiceTestSuite) Test_a_deployment_that_never_populated_the_identifier() {
	// The build wrote the link and the deploy did not write the value, which
	// is a broken deployment rather than a missing resource.
	svc := s.service(config.MapBackend{
		config.ResourcesNamespace: {},
	}, config.ResourcesNamespace).WithLinks(config.Links{
		"ordersBucket": {Type: "bucket", ConfigKey: "ORDERS_BUCKET_NAME"},
	})

	_, err := svc.Resource(context.Background(), "bucket", "ordersBucket")

	var missing *config.MissingKeyError
	s.Require().ErrorAs(err, &missing)
	s.Equal("ORDERS_BUCKET_NAME", missing.Key)
}

func (s *ServiceTestSuite) Test_a_store_the_backend_does_not_hold_is_not_an_empty_store() {
	// A deployment that did not happen, rather than a store with nothing in it.
	svc := s.service(config.MapBackend{}, "settings")

	_, err := svc.Get(context.Background(), "REGION")

	var missing *config.MissingStoreError
	s.Require().ErrorAs(err, &missing)
	s.Equal("settings", missing.StoreID)
}

func (s *ServiceTestSuite) Test_an_application_with_no_config_resource_reads_empty() {
	// EmptyBackend reports every store as empty rather than absent, so asking
	// for a value never declared reads as absent rather than as broken.
	svc := config.New()
	svc.Register("settings", config.NewNamespace(config.EmptyBackend{}, "settings"))

	_, ok, err := svc.Lookup(context.Background(), "REGION")

	s.Require().NoError(err)
	s.False(ok)
}
