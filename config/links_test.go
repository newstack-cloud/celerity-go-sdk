package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// The links file is written by the Celerity CLI at build time and is how a
// blueprint's name for a resource reaches the deployed thing's identifier.
type LinksTestSuite struct {
	suite.Suite
}

func TestLinksTestSuite(t *testing.T) {
	suite.Run(t, new(LinksTestSuite))
}

const linksJSON = `{
  "ordersBucket": {"type": "bucket", "configKey": "ORDERS_BUCKET_NAME"},
  "ordersQueue":  {"type": "queue",  "configKey": "ORDERS_QUEUE_URL"},
  "ordersTable":  {"type": "datastore", "configKey": "ORDERS_TABLE_NAME"}
}`

// write puts a links file in a directory of its own and points the SDK at it.
func (s *LinksTestSuite) write(contents string) string {
	dir := s.T().TempDir()
	path := filepath.Join(dir, config.LinksFilename)
	s.Require().NoError(os.WriteFile(path, []byte(contents), 0o600))
	s.T().Setenv(config.LinksPathEnvVar, path)
	return dir
}

func (s *LinksTestSuite) Test_the_topology_is_read_from_the_bundle() {
	s.write(linksJSON)

	links, err := config.LoadLinks()

	s.Require().NoError(err)
	s.Len(links, 3)
	s.Equal("bucket", links["ordersBucket"].Type)
	s.Equal("ORDERS_BUCKET_NAME", links["ordersBucket"].ConfigKey)
}

func (s *LinksTestSuite) Test_a_session_that_stages_no_bundle_passes_the_topology_directly() {
	// celerity dev has nothing to write a file into, so it sets the JSON in an
	// environment variable instead.
	s.T().Setenv(config.LinksPathEnvVar, filepath.Join(s.T().TempDir(), "nothing.json"))
	s.T().Setenv(config.LinksEnvVar, linksJSON)

	links, err := config.LoadLinks()

	s.Require().NoError(err)
	s.Equal("ORDERS_BUCKET_NAME", links["ordersBucket"].ConfigKey)
}

func (s *LinksTestSuite) Test_the_file_is_preferred_over_the_environment() {
	// The file is the contract for a built artefact, and the CLI always writes
	// it, so where both exist the bundle's own copy is the answer.
	s.write(linksJSON)
	s.T().Setenv(config.LinksEnvVar, `{"somethingElse": {"type": "queue", "configKey": "X"}}`)

	links, err := config.LoadLinks()

	s.Require().NoError(err)
	s.Contains(links, "ordersBucket")
	s.NotContains(links, "somethingElse")
}

func (s *LinksTestSuite) Test_no_topology_at_all_is_a_broken_build_rather_than_no_resources() {
	// Treating it as an application with no resources would turn a bundle that
	// was not built by the CLI into a handler that cannot find a bucket it does
	// declare.
	s.T().Setenv(config.LinksPathEnvVar, filepath.Join(s.T().TempDir(), "nothing.json"))
	s.T().Setenv(config.LinksEnvVar, "")

	_, err := config.LoadLinks()

	s.Require().Error(err)
	s.Contains(err.Error(), "celerity build", "the error should say what produces it")
	s.Contains(err.Error(), config.LinksEnvVar, "and what else it looked in")
}

func (s *LinksTestSuite) Test_a_malformed_file_is_refused() {
	s.write(`{not json`)

	_, err := config.LoadLinks()

	s.Require().Error(err)
	s.Contains(err.Error(), "not valid JSON")
}

func (s *LinksTestSuite) Test_resources_can_be_taken_by_kind() {
	s.write(linksJSON)
	links, err := config.LoadLinks()
	s.Require().NoError(err)

	buckets := links.OfKind("bucket")

	s.Len(buckets, 1)
	s.Contains(buckets, "ordersBucket")
}

func (s *LinksTestSuite) Test_a_handlers_scope_narrows_the_application() {
	// The links file describes every resource, because one bundle serves every
	// function, and is not a statement about any one handler.
	dir := s.write(linksJSON)
	scope := `{
	  "processOrder": ["ordersQueue", "ordersTable"],
	  "uploadReport": ["ordersBucket"]
	}`
	s.Require().NoError(os.WriteFile(
		filepath.Join(dir, config.ScopeFilename), []byte(scope), 0o600))

	got, known := config.LoadScope("processOrder")

	s.Require().True(known)
	s.True(got["ordersQueue"])
	s.True(got["ordersTable"])
	s.False(got["ordersBucket"], "a handler should see only what it reaches")
}

func (s *LinksTestSuite) Test_an_absent_scope_is_not_an_error() {
	cases := []struct {
		name      string
		handlerID string
		scope     string
	}{
		{
			// A bundle built before the extraction pass produced one has no
			// narrowing to apply, and the handler sees the whole application.
			name:      "no scope file",
			handlerID: "processOrder",
		},
		{
			name:      "a handler the scope file does not mention",
			handlerID: "somethingElse",
			scope:     `{"processOrder": ["ordersQueue"]}`,
		},
		{
			name:      "nothing said which handler this is",
			handlerID: "",
			scope:     `{"processOrder": ["ordersQueue"]}`,
		},
		{
			name:      "a malformed scope file",
			handlerID: "processOrder",
			scope:     `{not json`,
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			dir := s.write(linksJSON)
			if tc.scope != "" {
				s.Require().NoError(os.WriteFile(
					filepath.Join(dir, config.ScopeFilename), []byte(tc.scope), 0o600))
			}

			got, known := config.LoadScope(tc.handlerID)

			s.False(known)
			s.Nil(got)
		})
	}
}
