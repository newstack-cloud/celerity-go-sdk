package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// A test package reaching live resources has to link the providers, the same as
// the deployed binary does, because Go links what is imported and a test binary
// is a different binary from the one generate writes the main file for.
const liveFunc = "Live"

// celeritytestPath is the package that call belongs to.
const celeritytestPath = modulePath + "/celeritytest"

// LivePackage is a test package that reaches live resources, and where to write
// its imports.
type LivePackage struct {
	Name string
	Dir  string
}

// FileName is what the generated file is called in this package.
//
// Always a _test.go file, so what it links can never reach the deployed binary
// however the application is built.
func (p LivePackage) FileName() string {
	if strings.HasSuffix(p.Name, "_test") {
		return "celerity_live_gen_test.go"
	}

	return "celerity_live_gen_internal_test.go"
}

// Path is where the file is written.
func (p LivePackage) Path() string {
	return filepath.Join(p.Dir, p.FileName())
}

// DefaultTestTags is what an integration suite is built with, which is where a
// call to Live is, for tests that run against real services or service emulators
// running in a separate process.
const DefaultTestTags = "integration"

// LivePackages returns the test packages under root that call celeritytest.Live.
//
// Built with tags, because a suite reaching live resources is behind a build
// tag and a loader that did not set it would read the file as absent and write
// nothing, which is the silent failure worth guarding against here.
//
// A package that cannot be type-checked is an error rather than a package that
// calls nothing: writing to the packages that happened to load would leave the
// rest without their imports and fail later, further from the cause.
func LivePackages(root string, tags []string) ([]LivePackage, error) {
	loaded, err := packages.Load(
		&packages.Config{
			Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
				packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps |
				packages.NeedImports,
			Dir:        root,
			Tests:      true,
			BuildFlags: buildTags(tags),
		},
		"./...",
	)
	if err != nil {
		return nil, fmt.Errorf("loading the application's packages: %w", err)
	}

	if err := loadErrors(loaded); err != nil {
		return nil, err
	}

	found := map[LivePackage]bool{}
	for _, pkg := range loaded {
		dir, calls := callsLive(pkg)
		if !calls {
			continue
		}
		found[LivePackage{Name: pkg.Name, Dir: dir}] = true
	}

	return sorted(found), nil
}

// Reports whether a package calls celeritytest.Live, and the
// directory the file that does is in.
func callsLive(pkg *packages.Package) (string, bool) {
	if pkg.TypesInfo == nil {
		return "", false
	}

	for _, file := range pkg.Syntax {
		if !referencesLive(pkg, file) {
			continue
		}
		position := pkg.Fset.Position(file.Pos())
		if position.Filename == "" {
			continue
		}
		return filepath.Dir(position.Filename), true
	}

	return "", false
}

func referencesLive(pkg *packages.Package, file *ast.File) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		selector, isSelector := node.(*ast.SelectorExpr)
		if !isSelector || selector.Sel.Name != liveFunc || found {
			return !found
		}
		// Resolved rather than matched on the text, so that a variable or a
		// method of the application's own called Live is not taken for this one.
		fn, isFunc := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
		if isFunc && fn.Pkg() != nil && fn.Pkg().Path() == celeritytestPath {
			found = true
		}
		return !found
	})
	return found
}

func sorted(found map[LivePackage]bool) []LivePackage {
	out := make([]LivePackage, 0, len(found))
	for pkg := range found {
		out = append(out, pkg)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir < out[j].Dir
		}
		return out[i].Name < out[j].Name
	})

	return out
}

// ModuleRoot finds the directory holding go.mod at or above dir, which is where
// the packages are loaded from. The main package generate writes to is commonly
// a subdirectory, and the test packages are elsewhere again.
func ModuleRoot(dir string) (string, error) {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(absolute, "go.mod")); err == nil {
			return absolute, nil
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", fmt.Errorf("no go.mod at or above %s, so there is no module to scan", dir)
		}
		absolute = parent
	}
}

// What the loader is given so that a tagged suite is read.
func buildTags(tags []string) []string {
	named := make([]string, 0, len(tags))
	for _, tag := range tags {
		if trimmed := strings.TrimSpace(tag); trimmed != "" {
			named = append(named, trimmed)
		}
	}

	if len(named) == 0 {
		return nil
	}

	return []string{"-tags=" + strings.Join(named, ",")}
}
