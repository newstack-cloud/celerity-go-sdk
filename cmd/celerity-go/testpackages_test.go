package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	main_ "github.com/newstack-cloud/celerity-go-sdk/cmd/celerity-go"
)

type TestPackagesTestSuite struct {
	suite.Suite
	found []main_.LivePackage
}

func TestTestPackagesTestSuite(t *testing.T) {
	suite.Run(t, new(TestPackagesTestSuite))
}

func (s *TestPackagesTestSuite) SetupSuite() {
	s.T().Setenv("GOWORK", "off")

	found, err := main_.LivePackages("testdata/livetests", []string{main_.DefaultTestTags})
	s.Require().NoError(err)
	s.found = found
}

func (s *TestPackagesTestSuite) names() []string {
	out := make([]string, 0, len(s.found))
	for _, pkg := range s.found {
		out = append(out, pkg.Name)
	}
	return out
}

func (s *TestPackagesTestSuite) Test_a_test_package_calling_live_is_found() {
	s.Require().Len(s.found, 1, "one package reaches live resources")
	s.Equal("orders_test", s.found[0].Name,
		"the external test package's own clause, not the package under test's")
	s.Equal("orders", filepath.Base(s.found[0].Dir))
}

func (s *TestPackagesTestSuite) Test_a_package_testing_with_doubles_is_left_alone() {
	// Writing to it would link a cloud SDK into a test run that asked for none.
	s.NotContains(s.names(), "billing_test")
	s.NotContains(s.names(), "billing")
}

func (s *TestPackagesTestSuite) Test_an_application_with_a_live_of_its_own_is_not_mistaken_for_this_one() {
	// Applications can have a Live method or function that means something else, so matching the
	// selector on its text would write a cloud SDK into a package that never
	// needed one. What decides is the type checker resolving it to the SDK's own
	// function, and this is what stops that being simplified away.
	s.NotContains(s.names(), "lookalike")
	s.NotContains(s.names(), "lookalike_test")
}

func (s *TestPackagesTestSuite) Test_the_file_is_always_a_test_file() {
	// Production code should never be included in test package setup for live
	// resource loading for tests.
	for _, pkg := range []main_.LivePackage{{Name: "orders_test"}, {Name: "orders"}} {
		s.True(strings.HasSuffix(pkg.FileName(), "_test.go"), pkg.Name)
	}

	s.NotEqual(
		main_.LivePackage{Name: "orders"}.FileName(),
		main_.LivePackage{Name: "orders_test"}.FileName(),
		"a directory can hold both, so the two cannot share a file name",
	)
}

func (s *TestPackagesTestSuite) Test_what_is_written_is_what_the_main_file_links() {
	// The accuracy that matters the most, one generator, one list, so the test
	// binary cannot link a different set from the one deployed.
	opts := main_.GenerateOptions{Local: true, Resources: []string{"bucket"}}

	mainFile, err := main_.GeneratePlatformFile("main", main_.TargetAWSServerless, opts)
	s.Require().NoError(err)

	tagged := opts
	tagged.BuildTags = []string{main_.DefaultTestTags}
	testFile, err := main_.GeneratePlatformFile("orders_test", main_.TargetAWSServerless, tagged)
	s.Require().NoError(err)

	s.Equal(importsOf(mainFile), importsOf(testFile),
		"the same packages, whichever binary is being built")
}

func (s *TestPackagesTestSuite) Test_the_test_file_is_constrained_to_the_suite_it_serves() {
	opts := main_.GenerateOptions{Local: true, BuildTags: []string{"integration", "e2e"}}

	contents, err := main_.GeneratePlatformFile("orders_test", main_.TargetAWSServerless, opts)
	s.Require().NoError(err)

	s.True(strings.HasPrefix(contents, "//go:build integration || e2e\n\n"),
		"first in the file, and any of the tags rather than all of them")
}

func (s *TestPackagesTestSuite) Test_the_main_file_is_constrained_to_nothing() {
	contents, err := main_.GeneratePlatformFile("main", main_.TargetAWSServerless, main_.GenerateOptions{})

	s.Require().NoError(err)
	s.NotContains(contents, "//go:build", "it is needed by every build")
}

func (s *TestPackagesTestSuite) Test_a_module_root_is_found_from_a_package_below_it() {
	root, err := main_.ModuleRoot("testdata/livetests/orders")

	s.Require().NoError(err)
	s.FileExists(filepath.Join(root, "go.mod"))
	s.Equal("livetests", filepath.Base(root))
}

func (s *TestPackagesTestSuite) Test_a_directory_in_no_module_says_so() {
	_, err := main_.ModuleRoot(os.TempDir())

	s.Require().Error(err)
	s.Contains(err.Error(), "no go.mod")
}

// importsOf is the import paths a generated file links, which is what two
// generated files have to agree on.
func importsOf(contents string) []string {
	var paths []string
	for line := range strings.SplitSeq(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, isImport := strings.CutPrefix(trimmed, "_ \""); isImport {
			paths = append(paths, strings.TrimSuffix(after, "\""))
		}
	}
	return paths
}

// Writing the files is the part the command actually does, so it is asserted by
// doing it: against a copy of the fixture, since it writes into the tree it
// scans and a test must not leave files in testdata.
func (s *TestPackagesTestSuite) Test_the_imports_are_written_into_the_package_that_needs_them() {
	root := s.fixtureCopy()

	written, err := main_.GenerateTestPackageFiles(
		root, main_.TargetAWSServerless, []string{main_.DefaultTestTags},
		main_.GenerateOptions{Local: true})
	s.Require().NoError(err)

	s.Require().Len(written, 1)
	s.Equal(filepath.Join(root, "orders", "celerity_live_gen_test.go"), written[0])

	contents, err := os.ReadFile(written[0])
	s.Require().NoError(err)

	s.Contains(string(contents), "//go:build integration")
	s.Contains(string(contents), "package orders_test")
	s.Contains(string(contents), `_ "github.com/newstack-cloud/celerity-go-sdk/resources/local"`)
	s.Contains(string(contents), "DO NOT EDIT")
}

func (s *TestPackagesTestSuite) Test_nothing_is_written_into_the_packages_that_did_not_ask() {
	root := s.fixtureCopy()

	_, err := main_.GenerateTestPackageFiles(
		root, main_.TargetAWSServerless, []string{main_.DefaultTestTags},
		main_.GenerateOptions{Local: true})
	s.Require().NoError(err)

	for _, pkg := range []string{"billing", "lookalike"} {
		matches, err := filepath.Glob(filepath.Join(root, pkg, "celerity_*_gen*.go"))
		s.Require().NoError(err)
		s.Empty(matches, pkg)
	}
}

func (s *TestPackagesTestSuite) Test_writing_again_replaces_what_was_there() {
	// Regenerating is the ordinary case, since the CLI runs generate on every
	// build, and a stale file is one linking what the last target needed.
	root := s.fixtureCopy()
	target := filepath.Join(root, "orders", "celerity_live_gen_test.go")
	s.Require().NoError(os.WriteFile(target, []byte("package orders_test // stale\n"), 0o644))

	_, err := main_.GenerateTestPackageFiles(
		root, main_.TargetAWSServerless, []string{main_.DefaultTestTags},
		main_.GenerateOptions{Local: true})
	s.Require().NoError(err)

	contents, err := os.ReadFile(target)
	s.Require().NoError(err)
	s.NotContains(string(contents), "stale")
}

func (s *TestPackagesTestSuite) fixtureCopy() string {
	s.T().Helper()

	sdk, err := filepath.Abs(filepath.Join("..", ".."))
	s.Require().NoError(err)

	root := s.T().TempDir()
	s.Require().NoError(os.CopyFS(root, os.DirFS("testdata/livetests")))

	// The fixture's own replace is relative to where it sits in the repository.
	modfile := filepath.Join(root, "go.mod")
	contents, err := os.ReadFile(modfile)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(modfile,
		[]byte(strings.Replace(string(contents), "=> ../../../..", "=> "+sdk, 1)), 0o644))

	return root
}

func (s *TestPackagesTestSuite) Test_tags_that_cannot_be_a_build_constraint_are_refused() {
	// The go command refuses an unparseable //go:build line too, but it says so
	// at build time against a file marked DO NOT EDIT, which is the wrong place
	// to learn that a flag was wrong.
	for _, tags := range [][]string{
		{"integration", "my-tag"},
		{"two words"},
		{"integration", "e2e!"},
	} {
		_, err := main_.GeneratePlatformFile("orders_test", main_.TargetAWSServerless,
			main_.GenerateOptions{BuildTags: tags})

		s.Require().Error(err, tags)
		s.Contains(err.Error(), "cannot be a build constraint")
		s.Contains(err.Error(), "--test-tags", "naming where to fix it")
	}
}

func (s *TestPackagesTestSuite) Test_the_tags_a_suite_is_behind_are_matched_as_any_of() {
	// || rather than &&: a file is needed when any one of the suites that reach
	// live resources is being built, not only when all of them are.
	contents, err := main_.GeneratePlatformFile("orders_test", main_.TargetAWSServerless,
		main_.GenerateOptions{Local: true, BuildTags: []string{"integration", "e2e", "slow"}})

	s.Require().NoError(err)
	s.True(strings.HasPrefix(contents, "//go:build integration || e2e || slow\n\n"))
}
