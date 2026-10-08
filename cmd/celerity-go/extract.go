package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"

	"github.com/newstack-cloud/celerity-go-sdk/manifest"
)

// Extraction is two passes, because neither alone can answer the whole
// question.
//
// Running the application answers what it serves. The manifest comes from the
// same registry the dispatcher routes against, so a handler in it is by
// construction one the binary will serve. No analysis of a Go program can
// promise that, which is why the binary is run rather than read.
//
// Reading the source answers what each handler reaches. The handle a resource
// call produced is captured in a closure or held on a receiver, and Go offers no
// reflection into either. Which matters because that answer is what a
// deployment turns into IAM grants, so a resource missed here is a permission
// nobody asked for and a handler that fails on its first real event.

// ExtractEnvVar is what a binary is run with to make it report itself rather
// than serve.
const ExtractEnvVar = "CELERITY_EXTRACT_MANIFEST"

// Extract runs both passes against the application at pkg and answers the
// manifest the CLI reads.
func Extract(pkg string) (*manifest.Manifest, error) {
	built, cleanup, err := build(pkg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	m, err := report(built)
	if err != nil {
		return nil, err
	}

	reached, err := resolveReferences(pkg, handlerPaths(m))
	if err != nil {
		return nil, err
	}

	merge(m, reached)
	return m, nil
}

// build compiles the application into a binary to run, and answers how to
// clean it up.
//
// Built rather than `go run`, because a run mixes the compiler's own output
// into the program's and the manifest is read from stdout.
func build(pkg string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "celerity-extract-")
	if err != nil {
		return "", nil, fmt.Errorf("making somewhere to build: %w", err)
	}
	cleanup = func() {
		_ = os.RemoveAll(dir)
	}

	path = filepath.Join(dir, "app")
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = pkg
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf(
			"building %s: %w.\nExtraction builds the application and runs it, so it "+
				"has to compile first", pkg, err)
	}
	return path, cleanup, nil
}

// report runs the binary so that it describes itself.
func report(path string) (*manifest.Manifest, error) {
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), ExtractEnvVar+"=1")
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf(
			"running the application to read its handlers: %w.\nIt is run with %s set, "+
				"which celerity.Run answers by writing the manifest and exiting",
			err, ExtractEnvVar,
		)
	}

	var m manifest.Manifest
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf(
			"reading what the application reported: %w.\nIt wrote:\n%s", err, out,
		)
	}
	if m.Version != manifest.Version {
		return nil, fmt.Errorf(
			"the application reported a %s manifest, and this tool writes %s: "+
				"the SDK it was built against and this tool are different versions",
			m.Version, manifest.Version,
		)
	}
	return &m, nil
}

// What the static pass is asked to walk.
func handlerPaths(m *manifest.Manifest) []string {
	paths := make([]string, 0, len(m.FunctionHandlers))
	for _, handler := range m.FunctionHandlers {
		if handler.FuncPath != "" {
			paths = append(paths, handler.FuncPath)
		}
	}
	return paths
}

// Puts what the static pass found onto each handler, and takes the
// function paths off again.
//
// The union of the two, rather than one replacing the other: celerity.Uses is
// additive by design, for a resource the walk cannot see, so a handler that
// declared one keeps it alongside whatever was found.
func merge(m *manifest.Manifest, reached references) {
	for i := range m.FunctionHandlers {
		handler := &m.FunctionHandlers[i]

		names := declared(handler)
		for _, name := range reached[handler.FuncPath] {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)

		if len(names) > 0 {
			if handler.Annotations == nil {
				handler.Annotations = map[string]any{}
			}
			handler.Annotations[manifest.AnnotationResourceRef] = names
		}
		// The path was how the static pass knew what to walk, and what it
		// found is in the annotations now.
		handler.FuncPath = ""
	}
}

// declared is what celerity.Uses put on a handler, which the binary reported.
func declared(handler *manifest.FunctionHandler) []string {
	raw, held := handler.Annotations[manifest.AnnotationResourceRef]
	if !held {
		return nil
	}

	switch names := raw.(type) {
	case []string:
		return slices.Clone(names)
	case []any:
		// What it is after a round trip through JSON.
		declared := make([]string, 0, len(names))
		for _, name := range names {
			if text, isText := name.(string); isText {
				declared = append(declared, text)
			}
		}
		return declared
	default:
		return nil
	}
}
