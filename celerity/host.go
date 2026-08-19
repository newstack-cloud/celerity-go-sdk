package celerity

import (
	"sort"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// WithResourceProvider supplies the platform implementation behind the
// resource interfaces explicitly.
//
// Applications do not normally need it: importing a provider registers it, and
// the linked one is used. It is here for a test that wants to supply a fake.
func WithResourceProvider(p resources.Provider) Option {
	return func(o *options) { o.resourceProvider = p }
}

// ResourceProvider returns a provider given explicitly, satisfying
// [resources.Host]. Nil means the linked provider is used.
func (a *App) ResourceProvider() resources.Provider { return a.options.resourceProvider }

// RecordResourceRef notes that the application reaches a resource, satisfying
// [resources.Host].
//
// This is the application-wide set. Per-handler attribution comes from the
// static extraction pass, which is the only thing that can see through a
// closure; this set is what that pass is cross-checked against, so a reference
// the run saw and the analysis missed is reported rather than silently dropped.
func (a *App) RecordResourceRef(kind resources.Kind, name string) {
	if a.resourceRefs == nil {
		a.resourceRefs = make(map[resources.Kind]map[string]struct{})
	}
	if a.resourceRefs[kind] == nil {
		a.resourceRefs[kind] = make(map[string]struct{})
	}
	a.resourceRefs[kind][name] = struct{}{}
}

// ResourceError collects a resource that could not be built, satisfying
// [resources.Host].
func (a *App) ResourceError(err error) { a.errs = append(a.errs, err) }

// Extracting reports that this process is describing itself for the CLI rather
// than serving, satisfying [resources.Host].
func (a *App) Extracting() bool { return DetectMode() == ModeExtract }

// ResourceRefs returns every resource the application reaches, by kind, sorted.
func (a *App) ResourceRefs() map[resources.Kind][]string {
	refs := make(map[resources.Kind][]string, len(a.resourceRefs))
	for kind, names := range a.resourceRefs {
		list := make([]string, 0, len(names))
		for name := range names {
			list = append(list, name)
		}
		sort.Strings(list)
		refs[kind] = list
	}
	return refs
}
