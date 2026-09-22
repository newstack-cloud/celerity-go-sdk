// Command celerity-go is the build-time tool the Celerity CLI invokes for Go
// applications.
//
// It does the two things a compiled language needs doing before a Celerity
// application can be deployed, both of which the Node and Python SDKs get for
// free from being able to load code at runtime:
//
//	celerity-go generate --target aws-serverless --package ./cmd/app
//	celerity-go extract  --package ./cmd/app --out handler-manifest.json
//
// generate writes the imports that link the deploy target's platform packages
// into the binary, derived from the blueprint rather than written by hand.
// extract reports what the binary serves and which resources each handler
// reaches.
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

	contents, err := GeneratePlatformFile(packageName, *target, GenerateOptions{Local: *local})
	if err != nil {
		return err
	}

	path := filepath.Join(*pkg, GeneratedFileName)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Fprintf(os.Stderr, "celerity-go: wrote %s for the %q deploy target\n", path, *target)
	return nil
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

	// Extraction is two passes: build and run the application with
	// CELERITY_EXTRACT_MANIFEST set, then type-check the same packages to
	// resolve each handler's resource references, which running cannot answer
	// because that dependency is captured in a closure.
	return fmt.Errorf(
		"extract is not implemented yet: it will build %s, run it with "+
			"CELERITY_EXTRACT_MANIFEST=1, resolve resource references statically, and write %s",
		*pkg, *out,
	)
}
