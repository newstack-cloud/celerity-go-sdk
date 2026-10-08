// Command celerity-go is the build-time tool the Celerity CLI invokes for Go
// applications.
//
// It does the two things a compiled language needs doing before a Celerity
// application can be deployed, both of which the Node and Python SDKs get for
// free from being able to load code at runtime:
//
//	celerity-go generate --target aws-serverless --resource datastore,cache --package ./cmd/app
//	celerity-go extract  --package ./cmd/app --out handler-manifest.json
//
// generate writes the imports that link the deploy target's platform packages
// and the blueprint's database drivers into the binary, derived from the
// blueprint rather than written by hand.
// extract reports what the binary serves and which resources each handler
// reaches.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// version is stamped by scripts/package-cli.sh at release, from the tag.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "celerity-go: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("no command given")
	}

	switch args[0] {
	case "generate":
		return runGenerate(args[1:])
	case "extract":
		return runExtract(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	case "version", "--version":
		fmt.Println(version)
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `celerity-go %s builds Celerity Go applications for a deploy target.

Commands:
  generate   Write the platform imports for a deploy target
  extract    Produce the handler manifest for the Celerity CLI
  version    Print the version

Run celerity-go <command> --help for the flags each takes.
`, version)
}

func runGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	target := fs.String("target", os.Getenv("CELERITY_DEPLOY_TARGET"),
		"deploy target from the blueprint, such as aws-serverless")
	pkg := fs.String("package", ".", "directory of the application's main package")
	local := fs.Bool("local", false,
		"also link what a local development session reads, rather than only what the target deploys with")
	engines := fs.String("sql-engine", "",
		"database engines the blueprint declares, comma separated, so a driver is linked for each:\n"+
			"postgres, mysql, or engine=import/path to name a driver of your own")
	tracing := fs.String("telemetry", os.Getenv("CELERITY_TELEMETRY"),
		"what traces are exported with, so the module doing it is linked: otel, or none")
	testTags := fs.String("test-tags", DefaultTestTags,
		"build tags the integration suites are behind, comma separated, so the packages\n"+
			"that call celeritytest.Live are read rather than skipped")
	tests := fs.Bool("test-packages", false,
		"also write the imports into the test packages that call celeritytest.Live, so an\n"+
			"integration test links the providers the same way the deployed binary does")
	kinds := fs.String("resource", "",
		"resource kinds the blueprint declares, comma separated, so a package is linked for each:\n"+
			"bucket, queue, topic, cache, datastore, sqlDatabase")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" {
		return fmt.Errorf("no deploy target: pass --target or set CELERITY_DEPLOY_TARGET")
	}

	packageName, err := packageNameOf(*pkg)
	if err != nil {
		return err
	}

	// Checked before anything is written, so a bad --test-tags fails without
	// having changed the tree first.
	if *tests {
		if _, err := buildConstraint(splitList(*testTags)); err != nil {
			return err
		}
	}

	contents, err := GeneratePlatformFile(packageName, *target, GenerateOptions{
		Local:      *local,
		SQLEngines: splitList(*engines),
		Resources:  splitList(*kinds),
		Telemetry:  *tracing,
	})
	if err != nil {
		return err
	}

	path := filepath.Join(*pkg, GeneratedFileName)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Fprintf(os.Stderr, "celerity-go: wrote %s for the %q deploy target\n", path, *target)

	if !*tests {
		return nil
	}

	root, err := ModuleRoot(*pkg)
	if err != nil {
		return err
	}

	// The file just written imports modules the application does not require
	// yet, and the test packages cannot be type-checked until they resolve. So
	// this runs before the scan rather than being left to the caller, finding
	// which packages reach live resources is the whole point of this mode, and we
	// need to be able to type-check without errors.
	if err := tidy(root); err != nil {
		return err
	}

	written, err := GenerateTestPackageFiles(root, *target, splitList(*testTags), GenerateOptions{
		Local:      *local,
		SQLEngines: splitList(*engines),
		Resources:  splitList(*kinds),
		Telemetry:  *tracing,
	})
	if err != nil {
		return err
	}

	if len(written) == 0 {
		fmt.Fprintf(os.Stderr,
			"celerity-go: no test package calls celeritytest.Live, so none needed the providers\n")
	}

	for _, path := range written {
		fmt.Fprintf(os.Stderr, "celerity-go: wrote %s\n", path)
	}

	return nil
}

// GenerateTestPackageFiles writes the platform imports into every test package
// under root that reaches live resources, and returns what it wrote.
//
// The same options as the main file, through the same generator, so the two
// cannot disagree about what the application links. Constrained to the tags the
// suites are behind, so an ordinary test run links none of it.
//
// Expects the module to resolve already: see tidy, which the command runs
// first.
func GenerateTestPackageFiles(
	root, target string, tags []string, opts GenerateOptions,
) ([]string, error) {
	found, err := LivePackages(root, tags)
	if err != nil {
		return nil, err
	}

	tagged := opts
	tagged.BuildTags = tags

	written := make([]string, 0, len(found))
	for _, live := range found {
		contents, err := GeneratePlatformFile(live.Name, target, tagged)
		if err != nil {
			return nil, err
		}

		if err := os.WriteFile(live.Path(), []byte(contents), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", live.Path(), err)
		}

		written = append(written, live.Path())
	}
	return written, nil
}

// Reads a comma-separated flag, ignoring the empty entries a trailing
// comma or a flag passed empty by a caller building its arguments up leaves.
func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// packageNameOf reads the package clause the generated file has to match.
//
// It is read rather than assumed to be main: an application may keep its
// registrations in a package of its own and call into it from a thin main.
func packageNameOf(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == GeneratedFileName || filepath.Ext(name) != ".go" {
			continue
		}

		// Only the clause is parsed, so a file that does not compile yet, which
		// is ordinary mid-edit, still answers the question.
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.PackageClauseOnly)
		if err != nil {
			continue
		}
		return file.Name.Name, nil
	}

	return "", fmt.Errorf("no Go package found at %s", dir)
}

func runExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	pkg := fs.String("package", ".", "directory of the application's main package")
	out := fs.String("out", "handler-manifest.json", "where to write the manifest")

	if err := fs.Parse(args); err != nil {
		return err
	}

	m, err := Extract(*pkg)
	if err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("writing the manifest: %w", err)
	}
	if err := os.WriteFile(*out, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", *out, err)
	}

	fmt.Printf("wrote %s: %d handlers, %d guards\n",
		*out, len(m.FunctionHandlers), len(m.GuardHandlers))
	return nil
}

// tidy resolves the imports the generated file just added.
func tidy(root string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = root
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf(
			"go mod tidy in %s: %w.\nThe generated file imports modules the application "+
				"does not require yet, and they have to resolve before the test packages "+
				"can be read", root, err)
	}
	return nil
}
