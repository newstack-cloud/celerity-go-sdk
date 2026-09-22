package aws_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	awsconfig "github.com/newstack-cloud/celerity-go-sdk/config/aws"
)

// The parameters and secrets extension is a cache beside the function, so a
// warm invocation reads from localhost rather than paying a request to Secrets
// Manager. It is a cache in front of the service, which is what decides what
// should happen when it does not answer.
type ExtensionTestSuite struct {
	suite.Suite
}

func TestExtensionTestSuite(t *testing.T) {
	suite.Run(t, new(ExtensionTestSuite))
}

// fallbackBackend stands in for Secrets Manager behind the extension.
type fallbackBackend struct {
	called int
	values map[string]string
	err    error
}

func (b *fallbackBackend) Fetch(context.Context, string) (map[string]string, error) {
	b.called++
	return b.values, b.err
}

// extensionServing builds a backend talking to a stand-in extension, and
// returns what it was asked for.
func (s *ExtensionTestSuite) extensionServing(
	handler http.HandlerFunc,
	fallback config.Backend,
) config.Backend {
	server := httptest.NewServer(handler)
	s.T().Cleanup(server.Close)

	endpoint, err := url.Parse(server.URL)
	s.Require().NoError(err)

	// The backend reaches the extension on localhost at a port it is told, so
	// the stand-in's port is all it needs.
	return awsconfig.ExtensionBackend(endpoint.Port(), server.Client(), fallback)
}

func (s *ExtensionTestSuite) Test_the_extension_is_used_where_it_is_attached() {
	s.T().Setenv(awsconfig.ExtensionPortEnvVar, "2773")
	provider := awsconfig.ProviderWith(func(string) (*string, error) {
		s.Fail("Secrets Manager was called while the extension was attached")
		return nil, nil
	}, nil)

	backend, err := provider.Backend(awsconfig.StoreSecretsManager)

	s.Require().NoError(err)
	// It caches for itself, which is what stops the config package refreshing
	// on top of it: a refresh would re-read this cache rather than the store.
	caching, ok := backend.(config.SelfCaching)
	s.Require().True(ok, "the extension-backed backend should report that it caches")
	s.True(caching.Caching())
}

func (s *ExtensionTestSuite) Test_the_service_is_read_directly_where_it_is_not() {
	provider := awsconfig.ProviderWith(func(string) (*string, error) {
		value := `{"REGION":"eu-west-2"}`
		return &value, nil
	}, nil)

	backend, err := provider.Backend(awsconfig.StoreSecretsManager)
	s.Require().NoError(err)

	_, ok := backend.(config.SelfCaching)
	s.False(ok, "nothing is caching, so the config package should refresh")

	values, err := backend.Fetch(context.Background(), "orders-config")
	s.Require().NoError(err)
	s.Equal("eu-west-2", values["REGION"])
}

func (s *ExtensionTestSuite) Test_parameters_are_never_read_through_the_extension() {
	// The extension serves one secret or one parameter at a time and has no
	// equivalent of reading a whole path, which is how a store of parameters
	// is read.
	s.T().Setenv(awsconfig.ExtensionPortEnvVar, "2773")
	provider := awsconfig.ProviderWith(nil, func(string, *string) (map[string]string, *string, error) {
		return map[string]string{"/orders/A": "1"}, nil, nil
	})

	backend, err := provider.Backend(awsconfig.StoreParameterStore)
	s.Require().NoError(err)

	values, err := backend.Fetch(context.Background(), "/orders")

	s.Require().NoError(err)
	s.Equal(map[string]string{"A": "1"}, values)
}

func (s *ExtensionTestSuite) Test_a_secret_is_read_from_the_extensions_cache() {
	var token, asked string
	backend := s.extensionServing(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("X-Aws-Parameters-Secrets-Token")
		asked = r.URL.Query().Get("secretId")
		_, _ = w.Write([]byte(`{"SecretString":"{\"REGION\":\"eu-west-2\"}"}`))
	}, &fallbackBackend{})

	s.T().Setenv(awsconfig.SessionTokenEnvVar, "session-token-123")

	values, err := backend.Fetch(context.Background(), "orders/config")

	s.Require().NoError(err)
	s.Equal("eu-west-2", values["REGION"])
	s.Equal("orders/config", asked, "the store name should survive being put in a query")
	// Required by the extension, so that only the function the credentials
	// belong to can read its secrets.
	s.Equal("session-token-123", token)
}

func (s *ExtensionTestSuite) Test_the_service_is_read_when_the_extension_does_not_answer() {
	// The extension caches Secrets Manager, so anything wrong with it is a
	// reason to read the thing it caches rather than to fail: the values are
	// still there.
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "it refuses the request",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
		},
		{
			name: "it answers with something else",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`not json`))
			},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			fallback := &fallbackBackend{values: map[string]string{"REGION": "eu-west-1"}}
			backend := s.extensionServing(tc.handler, fallback)

			values, err := backend.Fetch(context.Background(), "orders-config")

			s.Require().NoError(err)
			s.Equal(1, fallback.called, "the service should have been read instead")
			s.Equal("eu-west-1", values["REGION"])
		})
	}
}

func (s *ExtensionTestSuite) Test_a_secret_the_extension_holds_nothing_for_is_empty() {
	fallback := &fallbackBackend{}
	backend := s.extensionServing(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}, fallback)

	values, err := backend.Fetch(context.Background(), "orders-config")

	s.Require().NoError(err)
	s.Empty(values)
	s.Equal(0, fallback.called, "an empty store is an answer, not a failure to fall back from")
}

func (s *ExtensionTestSuite) Test_a_failure_behind_the_extension_is_still_a_failure() {
	// Falling back is not a way of hiding that the configuration cannot be
	// read at all.
	fallback := &fallbackBackend{err: errors.New("AccessDeniedException")}
	backend := s.extensionServing(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}, fallback)

	_, err := backend.Fetch(context.Background(), "orders-config")

	s.Require().Error(err)
	s.Contains(err.Error(), "AccessDeniedException")
}
