package resources_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// A provider is selected by being imported, so what is linked is decided before
// the program runs and cannot be changed after. What this covers is the two
// things that decision leaves open: a duplicate, and more than one.
type RegistryTestSuite struct {
	suite.Suite
}

func TestRegistryTestSuite(t *testing.T) {
	suite.Run(t, new(RegistryTestSuite))
}

func (s *RegistryTestSuite) SetupTest()    { resources.ResetProviders() }
func (s *RegistryTestSuite) TearDownTest() { resources.ResetProviders() }

// A provider that does nothing but says what it is, which is
// all that selection depends on.
type namedProvider struct {
	mockProvider
	name string
	here bool
}

func (p *namedProvider) Name() string {
	return p.name
}

// holdingProvider holds something worth giving back, which only providers with
// a pool or a connection implement.
type holdingProvider struct {
	namedProvider
	closed int
	err    error
}

func (p *holdingProvider) Close(context.Context) error {
	p.closed++
	return p.err
}

// detectingProvider reports whether this is its platform, which only providers
// that can tell implement.
type detectingProvider struct {
	namedProvider
}

func (p *detectingProvider) Detect() bool {
	return p.here
}

func (s *RegistryTestSuite) Test_the_only_linked_provider_is_used_whatever_the_environment() {
	// A binary carrying one provider is being run against that platform,
	// including from a developer's machine where none of its own variables are
	// set.
	only := &detectingProvider{namedProvider{name: "aws"}}
	resources.RegisterProvider(only)

	found, ok := resources.DetectedProvider()

	s.Require().True(ok)
	s.Same(only, found, "with one linked, it should not have been asked to detect")
}

func (s *RegistryTestSuite) Test_the_environment_decides_between_several() {
	elsewhere := &detectingProvider{namedProvider{name: "gcp"}}
	here := &detectingProvider{namedProvider{name: "aws", here: true}}
	resources.RegisterProvider(elsewhere)
	resources.RegisterProvider(here)

	found, ok := resources.DetectedProvider()

	s.Require().True(ok)
	s.Same(here, found)
}

func (s *RegistryTestSuite) Test_several_linked_and_none_matching_selects_nothing() {
	// Guessing would build clients against the wrong platform, which fails
	// somewhere much further from the cause than here.
	resources.RegisterProvider(&detectingProvider{namedProvider{name: "gcp"}})
	resources.RegisterProvider(&detectingProvider{namedProvider{name: "azure"}})

	_, ok := resources.DetectedProvider()

	s.False(ok)
}

func (s *RegistryTestSuite) Test_a_provider_registered_twice_is_a_mistake_in_the_build() {
	// Two packages registering the same name means one of them is not what it
	// says, and whichever won would be a silent choice between them.
	resources.RegisterProvider(&namedProvider{name: "aws"})

	s.Panics(func() {
		resources.RegisterProvider(&namedProvider{name: "aws"})
	})
}

func (s *RegistryTestSuite) Test_what_is_linked_is_reported_for_the_manifest() {
	// Go links one artefact, so a deployment targeting a platform the binary
	// carries no provider for cannot be fixed by configuration. The CLI refuses
	// that at build time from this.
	resources.RegisterProvider(&namedProvider{name: "gcp"})
	resources.RegisterProvider(&namedProvider{name: "aws"})

	s.Equal([]string{"aws", "gcp"}, resources.RegisteredProviders())
}

func (s *RegistryTestSuite) Test_a_missing_provider_names_what_was_linked_instead() {
	resources.RegisterProvider(&namedProvider{name: "gcp"})
	resources.RegisterProvider(&namedProvider{name: "azure"})
	host := &mockHost{}

	resources.Bucket(host, "ordersBucket")

	s.Require().Len(host.errs, 1)
	s.Contains(host.errs[0].Error(), "More than one provider is linked")
	s.Contains(host.errs[0].Error(), "azure, gcp")
}

func (s *RegistryTestSuite) Test_what_is_held_is_given_back_on_shutdown() {
	holding := &holdingProvider{namedProvider: namedProvider{name: "aws"}}
	resources.RegisterProvider(holding)

	s.Require().NoError(resources.Release(context.Background()))

	s.Equal(1, holding.closed)
}

func (s *RegistryTestSuite) Test_one_provider_failing_to_release_does_not_stop_the_rest() {
	failing := &holdingProvider{
		namedProvider: namedProvider{name: "aws"},
		err:           errors.New("the pool would not drain"),
	}
	other := &holdingProvider{namedProvider: namedProvider{name: "gcp"}}
	resources.RegisterProvider(failing)
	resources.RegisterProvider(other)

	err := resources.Release(context.Background())

	s.Require().Error(err)
	s.Contains(err.Error(), "would not drain")
	s.Equal(1, other.closed, "the second should have been released too")
}
