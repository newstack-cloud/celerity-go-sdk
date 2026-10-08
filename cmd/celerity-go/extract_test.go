package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/manifest"
)

// Extraction is asserted against a real application rather than a stand-in,
// because what it has to get right is what the Go compiler and runtime actually
// produce: how a method value is named, how function literals are numbered, and
// where a handle ends up. A fixture that only looked like an application would
// agree with whatever this implementation happened to do.
type ExtractTestSuite struct {
	suite.Suite
	extracted *manifest.Manifest
}

func TestExtractTestSuite(t *testing.T) {
	suite.Run(t, new(ExtractTestSuite))
}

func (s *ExtractTestSuite) SetupSuite() {
	// The fixture is its own module inside this repository's workspace, which
	// a real application would not be, so the workspace is turned off for it.
	s.T().Setenv("GOWORK", "off")

	extracted, err := Extract("testdata/app")
	s.Require().NoError(err)
	s.extracted = extracted
}

// reaches is what a handler was recorded as reaching.
func (s *ExtractTestSuite) reaches(name string) []string {
	s.T().Helper()

	for _, handler := range s.extracted.FunctionHandlers {
		if handler.ResourceName != name {
			continue
		}
		raw, held := handler.Annotations[manifest.AnnotationResourceRef]
		if !held {
			return nil
		}
		names, isNames := raw.([]string)
		s.Require().True(isNames, "the annotation is a list of names")
		return names
	}
	s.FailNowf("no such handler", "nothing registered as %q", name)
	return nil
}

func (s *ExtractTestSuite) Test_running_the_binary_reports_what_it_serves() {
	// The first pass, which is why the binary is run rather than read: a
	// handler in the manifest is by construction one the dispatcher routes to.
	names := make([]string, 0, len(s.extracted.FunctionHandlers))
	for _, handler := range s.extracted.FunctionHandlers {
		names = append(names, handler.ResourceName)
	}

	s.ElementsMatch(
		[]string{"createOrder", "getUpload", "archiveUpload", "health", "announce"},
		names,
	)
}

func (s *ExtractTestSuite) Test_a_handle_on_a_receiver_is_found_through_the_method_value() {
	// Registered as service.create, which the compiler names
	// main.(*orders).create-fm: a wrapper around the same body.
	s.ElementsMatch([]string{"ordersTable", "workQueue"}, s.reaches("createOrder"))
}

func (s *ExtractTestSuite) Test_a_call_a_handler_makes_is_followed() {
	// workQueue is reached only by notify, which create calls, so finding it
	// needs the walk to go into the function rather than stop at the body.
	s.Contains(s.reaches("createOrder"), "workQueue")
}

func (s *ExtractTestSuite) Test_a_handle_a_closure_captured_is_found() {
	s.Equal([]string{"uploadsBucket"}, s.reaches("getUpload"))
}

func (s *ExtractTestSuite) Test_a_name_that_is_a_constant_is_folded() {
	// A literal is the ordinary case, but a constant is still something the
	// type checker can tell the value of.
	s.ElementsMatch([]string{"archiveBucket", "uploadsBucket"}, s.reaches("archiveUpload"))
}

func (s *ExtractTestSuite) Test_a_handler_that_reaches_nothing_records_nothing() {
	// Rather than an empty list, which a deployment would have to know meant
	// the same thing.
	s.Nil(s.reaches("health"))
}

func (s *ExtractTestSuite) Test_what_uses_declared_is_kept_alongside_what_was_found() {
	// Uses is additive by design, for a resource the walk cannot see, so a
	// handler that declared one keeps it.
	reached := s.reaches("announce")

	s.Contains(reached, "auditBucket", "declared with celerity.Uses")
	s.Contains(reached, "orderEvents", "and found by following the call")
}

func (s *ExtractTestSuite) Test_the_function_path_is_not_handed_to_the_cli() {
	// It was how the static pass knew what to walk. What it found is in the
	// annotations by the time the manifest is written.
	for _, handler := range s.extracted.FunctionHandlers {
		s.Empty(handler.FuncPath, handler.ResourceName)
	}
}

func (s *ExtractTestSuite) Test_an_application_that_does_not_compile_says_so() {
	s.T().Setenv("GOWORK", "off")

	_, err := Extract("testdata/broken")

	s.Require().Error(err)
	s.Contains(err.Error(), "has to compile first")
}

func (s *ExtractTestSuite) Test_a_resource_named_by_something_that_is_not_constant_is_refused() {
	// Dropping it would mean a deployment granting access to everything but
	// that one, which fails at runtime as a permission nobody asked about.
	s.T().Setenv("GOWORK", "off")

	_, err := Extract("testdata/nonconstant")

	s.Require().Error(err)
	s.Contains(err.Error(), "is not a constant")
	s.Contains(err.Error(), "celerity.Uses", "naming the way out")
}

// A handler rarely holds its resources itself. It calls a service that calls
// another, is given one by a constructor, or reaches one through an interface,
// and the handle is two or three steps from the body the runtime names. Each of
// these shapes was missed until it was here.
type IndirectTestSuite struct {
	suite.Suite
	extracted *manifest.Manifest
}

func TestIndirectTestSuite(t *testing.T) {
	suite.Run(t, new(IndirectTestSuite))
}

func (s *IndirectTestSuite) SetupSuite() {
	s.T().Setenv("GOWORK", "off")

	extracted, err := Extract("testdata/indirect")
	s.Require().NoError(err)
	s.extracted = extracted
}

func (s *IndirectTestSuite) reaches(name string) []string {
	s.T().Helper()

	for _, handler := range s.extracted.FunctionHandlers {
		if handler.ResourceName != name {
			continue
		}
		names, _ := handler.Annotations[manifest.AnnotationResourceRef].([]string)
		return names
	}
	s.FailNowf("no such handler", "nothing registered as %q", name)
	return nil
}

func (s *IndirectTestSuite) Test_a_chain_of_services_is_followed_to_the_end() {
	// ordersTable is two calls away and auditBucket three, each held on a
	// different service's receiver.
	s.ElementsMatch([]string{"auditBucket", "ordersTable"}, s.reaches("createOrder"))
}

func (s *IndirectTestSuite) Test_a_handle_that_arrives_as_a_constructor_argument_is_followed() {
	// The field is assigned the parameter, so nothing connects it to the queue
	// except the call that passed one in.
	s.Equal([]string{"shippingQueue"}, s.reaches("shipOrder"))
}

func (s *IndirectTestSuite) Test_a_factory_answering_with_a_closure_is_followed() {
	// The handler calls a variable, and the body that captured the handle is
	// inside the function that returned it, so neither the call site nor the
	// variable names anything with a body of its own.
	s.Equal([]string{"priceCache"}, s.reaches("priceOrder"))
}

func (s *IndirectTestSuite) Test_a_factory_called_through_a_variable_is_followed() {
	// Without resolving the variable to the function, the factory's parameters
	// bind to nothing and the field it stores them in holds nothing.
	s.Equal([]string{"reportBucket"}, s.reaches("reportOrder"))
}

func (s *IndirectTestSuite) Test_a_constructor_that_can_fail_is_followed_to_what_it_answered() {
	// Two values back means one expression on the right of the assignment and
	// two names on the left, which the collection used to skip entirely. The
	// handle is connected only by what the constructor returns, so reading the
	// return statement is what resolves it.
	s.Equal([]string{"ledgerBucket"}, s.reaches("writeLedger"))
}

func (s *IndirectTestSuite) Test_a_service_behind_an_interface_is_followed() {
	// The call site names the interface, which has no body to walk, so what the
	// application's own implementations reach is what the handler reaches.
	s.Equal([]string{"orderEvents"}, s.reaches("announceOrder"))
}
