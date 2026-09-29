package resources_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Handles are taken while an application is being registered, which is before
// anything has run and before any store has been read. What that costs, and
// what is deferred until a handler actually uses one, is what this covers.
type HandlesTestSuite struct {
	suite.Suite
}

func TestHandlesTestSuite(t *testing.T) {
	suite.Run(t, new(HandlesTestSuite))
}

// mockHost stands in for *celerity.App, which this package cannot import.
type mockHost struct {
	provider   resources.Provider
	config     *config.Service
	refs       []resources.Ref
	errs       []error
	extracting bool
}

func (h *mockHost) ResourceProvider() resources.Provider { return h.provider }
func (h *mockHost) Config() *config.Service              { return h.config }
func (h *mockHost) ResourceError(err error)              { h.errs = append(h.errs, err) }
func (h *mockHost) Extracting() bool                     { return h.extracting }

func (h *mockHost) RecordResourceRef(kind resources.Kind, name string) {
	h.refs = append(h.refs, resources.Ref{Kind: kind, Name: name})
}

// mockProvider records the refs it was asked to build from, which is what a
// real provider holds on to rather than resolving.
type mockProvider struct {
	built []resources.Ref
	err   error
}

func (p *mockProvider) Name() string { return "mock" }

func (p *mockProvider) Bucket(r resources.Ref) (bucket.Store, error) {
	return nil, p.record(r)
}
func (p *mockProvider) Queue(r resources.Ref) (queue.Client, error) {
	return nil, p.record(r)
}
func (p *mockProvider) Topic(r resources.Ref) (topic.Client, error) {
	return nil, p.record(r)
}
func (p *mockProvider) Cache(r resources.Ref) (cache.Client, error) {
	return nil, p.record(r)
}
func (p *mockProvider) Datastore(r resources.Ref) (datastore.Client, error) {
	return nil, p.record(r)
}
func (p *mockProvider) SQLDatabase(r resources.Ref) (sqldb.Client, error) {
	return nil, p.record(r)
}

func (p *mockProvider) record(r resources.Ref) error {
	p.built = append(p.built, r)
	return p.err
}

func (s *HandlesTestSuite) Test_a_handle_is_taken_by_the_name_the_blueprint_gave_it() {
	cases := []struct {
		name string
		take func(host resources.Host)
		kind resources.Kind
	}{
		{"bucket", func(h resources.Host) { resources.Bucket(h, "ordersBucket") }, resources.KindBucket},
		{"queue", func(h resources.Host) { resources.Queue(h, "ordersQueue") }, resources.KindQueue},
		{"topic", func(h resources.Host) { resources.Topic(h, "ordersTopic") }, resources.KindTopic},
		{"cache", func(h resources.Host) { resources.Cache(h, "ordersCache") }, resources.KindCache},
		{"datastore", func(h resources.Host) { resources.Datastore(h, "ordersTable") }, resources.KindDatastore},
		{"sql database", func(h resources.Host) { resources.SQLDatabase(h, "ordersDb") }, resources.KindSQLDatabase},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			provider := &mockProvider{}
			host := &mockHost{provider: provider, config: config.New()}

			tc.take(host)

			s.Require().Len(provider.built, 1)
			s.Equal(tc.kind, provider.built[0].Kind)
			s.Same(host.config, provider.built[0].Config)
			s.Empty(host.errs)
		})
	}
}

func (s *HandlesTestSuite) Test_nothing_is_resolved_while_a_handle_is_being_taken() {
	// A store that fails every read. Taking a handle through it has to be
	// silent, or a list of registrations would be a list of config requests.
	cfg := config.New().WithLinks(config.Links{
		"ordersBucket": {Type: "bucket", ConfigKey: "ordersBucketName"},
	})
	cfg.Register(config.ResourcesNamespace, config.NewNamespace(
		failingBackend{}, "resources"))

	provider := &mockProvider{}
	host := &mockHost{provider: provider, config: cfg}

	resources.Bucket(host, "ordersBucket")

	s.Empty(host.errs, "taking a handle should not have read the store")
	s.Require().Len(provider.built, 1)

	// And the same ref does fail once something asks it, so the store really
	// was unreadable and the silence above was deferral rather than success.
	_, err := provider.built[0].ID(context.Background())
	s.Require().Error(err)
}

func (s *HandlesTestSuite) Test_a_reference_is_recorded_even_where_no_provider_can_build_it() {
	// Extraction runs with no provider linked, and the reference is the whole
	// point of that run: it is what becomes the handler's IAM grants.
	host := &mockHost{config: config.New(), extracting: true}

	resources.Bucket(host, "ordersBucket")

	s.Equal([]resources.Ref{{Kind: resources.KindBucket, Name: "ordersBucket"}}, host.refs)
	s.Empty(host.errs, "a missing provider is expected while extracting")
}

func (s *HandlesTestSuite) Test_a_missing_provider_is_reported_when_serving() {
	resources.ResetProviders()
	host := &mockHost{config: config.New()}

	resources.Bucket(host, "ordersBucket")

	s.Require().Len(host.errs, 1)
	s.Contains(host.errs[0].Error(), "ordersBucket")
	s.Contains(host.errs[0].Error(), "resources/aws",
		"the error should name the import that would fix it")
}

func (s *HandlesTestSuite) Test_a_handle_taken_without_a_name_means_the_only_one_declared() {
	provider := &mockProvider{}
	host := &mockHost{provider: provider, config: config.New()}

	resources.Bucket(host)

	s.Require().Len(provider.built, 1)
	s.Equal(resources.DefaultName, provider.built[0].Name)
}

func (s *HandlesTestSuite) Test_a_provider_that_cannot_build_a_client_is_reported_by_resource() {
	provider := &mockProvider{err: errNoCredentials}
	host := &mockHost{provider: provider, config: config.New()}

	resources.Queue(host, "ordersQueue")

	s.Require().Len(host.errs, 1)
	s.Contains(host.errs[0].Error(), `queue "ordersQueue"`)
	s.ErrorIs(host.errs[0], errNoCredentials)
}
