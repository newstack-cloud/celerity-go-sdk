package service_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
)

type RegistryTestSuite struct {
	suite.Suite
}

func TestRegistryTestSuite(t *testing.T) {
	suite.Run(t, new(RegistryTestSuite))
}

// A build links the resource packages the blueprint calls for. Reaching a kind
// whose package is not linked has to say which import is missing, because the
// alternative is a nil client failing later with nothing to say about why.
func (s *RegistryTestSuite) Test_an_unlinked_resource_package_names_its_import() {
	// No resource package is imported by this test binary, so every kind is
	// unregistered, which is the state a build with a stale generated file is
	// in for the kind it left out.
	_, err := service.Datastores(service.NewSession())

	s.Require().Error(err)
	s.Contains(err.Error(), "resources/aws/datastore",
		"the fix is the import, so the error is the import")
	s.Contains(err.Error(), "celerity-go generate",
		"and what would have written it")
}

func (s *RegistryTestSuite) Test_every_kind_reports_its_own_package() {
	session := service.NewSession()
	cases := []struct {
		name string
		find func() error
		pkg  string
	}{
		{"bucket", func() error { _, err := service.Buckets(session); return err }, "aws/bucket"},
		{"queue", func() error { _, err := service.Queues(session); return err }, "aws/queue"},
		{"topic", func() error { _, err := service.Topics(session); return err }, "aws/topic"},
		{"cache", func() error { _, err := service.Caches(session); return err }, "aws/cache"},
		{"datastore", func() error { _, err := service.Datastores(session); return err }, "aws/datastore"},
		{"sqldb", func() error { _, err := service.SQLDatabases(session); return err }, "aws/sqldb"},
	}

	for _, test := range cases {
		s.Run(test.name, func() {
			err := test.find()

			s.Require().Error(err)
			s.Contains(err.Error(), test.pkg)
		})
	}
}
