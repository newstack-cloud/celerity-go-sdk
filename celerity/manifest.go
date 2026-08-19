package celerity

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	"github.com/newstack-cloud/celerity-go-sdk/manifest"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/serverless"
)

// Manifest builds the handler manifest for this application.
//
// Resource references are left to the extraction tool's static pass: a
// reference captured in a closure is not reachable from a running program, so
// filling the annotation in here would report only what happened to be visible
// and would look complete while being wrong.
func (a *App) Manifest() *manifest.Manifest {
	root := moduleRoot()
	m := &manifest.Manifest{
		Version:                 manifest.Version,
		Handlers:                []manifest.ClassHandler{},
		FunctionHandlers:        []manifest.FunctionHandler{},
		GuardHandlers:           []manifest.GuardHandler{},
		LinkedAdapters:          serverless.Registered(),
		LinkedResourceProviders: resources.RegisteredProviders(),
	}

	for _, reg := range a.registry.All() {
		m.FunctionHandlers = append(m.FunctionHandlers, manifest.FunctionHandler{
			ResourceName: reg.Name,
			ExportName:   reg.Name,
			SourceFile:   relativeTo(root, reg.SourceFile),
			Annotations:  annotationsFor(reg),
			FuncPath:     reg.FuncPath,
			Spec: manifest.Spec{
				HandlerName: reg.PublishedName,
				Handler:     reg.Name,
			},
		})
	}

	return m
}

func (a *App) writeManifest(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(a.Manifest())
}

func annotationsFor(reg *Registration) map[string]any {
	annotations := map[string]any{manifest.AnnotationTag: reg.Tag}

	switch reg.Kind {
	case handler.KindHTTP:
		annotations[manifest.AnnotationHTTP] = true
		annotations[manifest.AnnotationHTTPMethod] = reg.Method
		annotations[manifest.AnnotationHTTPPath] = reg.Route
	case handler.KindWebSocket:
		annotations[manifest.AnnotationWebSocket] = true
		annotations[manifest.AnnotationWebSocketRoute] = reg.Route
	case handler.KindConsumer:
		annotations[manifest.AnnotationConsumer] = true
		annotations[manifest.AnnotationConsumerSource] = reg.SourceID
	case handler.KindSchedule:
		annotations[manifest.AnnotationSchedule] = true
	}

	if len(reg.Guards) > 0 {
		annotations[manifest.AnnotationGuardProtectedBy] = reg.Guards
	}
	if reg.Public {
		annotations[manifest.AnnotationPublic] = true
	}
	if len(reg.Uses) > 0 {
		annotations[manifest.AnnotationResourceRef] = reg.Uses
	}

	return annotations
}

// moduleRoot finds the directory source paths should be reported relative to.
//
// Registration captures absolute paths from runtime.Caller, and an absolute
// path from a build machine means nothing to whoever reads the manifest.
func moduleRoot() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ""
		}
		return wd
	}
	return info.Main.Path
}

func relativeTo(root, path string) string {
	if root == "" || path == "" {
		return path
	}
	// A module path prefix, github.com/acme/app, appears in the middle of the
	// build machine's absolute path rather than at its start.
	if i := strings.Index(path, root); i >= 0 {
		return strings.TrimPrefix(path[i+len(root):], string(filepath.Separator))
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
