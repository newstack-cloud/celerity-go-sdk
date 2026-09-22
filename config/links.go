package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Files the Celerity CLI writes into the handler bundle at build time.
//
// Carried as files rather than as environment variables because one bundle
// serves every function in the application, and the topology of a large one
// does not fit in an environment a platform will accept.
const (
	// LinksFilename maps every resource the application declares to the config
	// key holding its identifier.
	LinksFilename = "__celerity_resource_links__.json"
	// ScopeFilename says which of those resources each handler actually uses,
	// so a handler sees only what it reaches. Produced by the extraction pass.
	ScopeFilename = "__celerity_handler_scope__.json"
)

// LinksPathEnvVar points the SDK at the links file somewhere other than beside
// the handler.
const LinksPathEnvVar = "CELERITY_RESOURCE_LINKS_PATH"

// LinksEnvVar carries the topology itself, which is how a local development
// session provides it, that path doesn't stage any files, having nothing to stage them
// into, so it passes the JSON directly.
const LinksEnvVar = "CELERITY_RESOURCE_LINKS"

// Link is one resource in the topology.
type Link struct {
	// Type is the resource's kind, in the vocabulary the resources package
	// uses: bucket, queue, topic, datastore, cache, sqlDatabase.
	Type string `json:"type"`
	// ConfigKey is the key in the resources namespace holding the identifier
	// this resource was deployed under.
	ConfigKey string `json:"configKey"`
}

// Links is the resource topology, by the name the blueprint gave each resource.
type Links map[string]Link

// Find returns the link for a resource of a kind.
//
// The kind is checked rather than trusted: a name that belongs to a queue
// asked for as a bucket is a mistake in the handler, and resolving it would
// hand back a queue's URL to something about to treat it as a bucket.
func (l Links) Find(kind, name string) (Link, error) {
	link, ok := l[name]
	if !ok {
		return Link{}, &MissingLinkError{
			Kind:  kind,
			Name:  name,
			Known: l.namesOf(kind),
		}
	}

	if link.Type != kind {
		return Link{}, fmt.Errorf(
			"celerity: resource %q is a %s, and was asked for as a %s",
			name, link.Type, kind,
		)
	}
	return link, nil
}

// OfKind returns the resources of one kind, by name.
func (l Links) OfKind(kind string) Links {
	out := make(Links)
	for name, link := range l {
		if link.Type == kind {
			out[name] = link
		}
	}
	return out
}

func (l Links) namesOf(kind string) []string {
	var names []string
	for name, link := range l {
		if link.Type == kind {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// MissingLinkError reports a resource the topology does not hold.
type MissingLinkError struct {
	Kind  string
	Name  string
	Known []string
}

func (e *MissingLinkError) Error() string {
	if len(e.Known) == 0 {
		return fmt.Sprintf(
			"celerity: the application declares no %s, and %q was asked for",
			e.Kind, e.Name,
		)
	}
	return fmt.Sprintf(
		"celerity: no %s named %q, the blueprint declares: %v",
		e.Kind, e.Name, e.Known,
	)
}

// LoadLinks reads the topology the Celerity CLI produced.
//
// The file is the contract for a built artifact. The CLI writes it into the
// bundle, always, holding an empty object where the blueprint declares nothing,
// so that an SDK reading it has one case to handle.
//
// A development session does not stage a bundle to put a file in, and passes the
// topology in [LinksEnvVar] instead, which is read where there is no file.
//
// Neither is an error rather than an empty topology. Treating an absent
// topology as an application with no resources would turn a bundle that was not
// built by the CLI into a handler that cannot find a bucket it does declare.
func LoadLinks() (Links, error) {
	path := linksPath()

	raw, err := os.ReadFile(path)
	if err == nil {
		return parseLinks(raw, path)
	}

	if inline := os.Getenv(LinksEnvVar); inline != "" {
		return parseLinks([]byte(inline), LinksEnvVar)
	}

	return nil, fmt.Errorf(
		"celerity: no resource links at %s and none in %s: %w "+
			"(written by celerity build, and set by celerity dev)",
		path, LinksEnvVar, err,
	)
}

func parseLinks(raw []byte, source string) (Links, error) {
	var links Links
	if err := json.Unmarshal(raw, &links); err != nil {
		return nil, fmt.Errorf("celerity: the resource links in %s are not valid JSON: %w", source, err)
	}
	return links, nil
}

func linksPath() string {
	if path := os.Getenv(LinksPathEnvVar); path != "" {
		return path
	}
	return LinksFilename
}

// LoadScope returns the resources one handler uses, and whether that is known.
//
// The links file describes the whole application, because one bundle serves
// every function, and is not a statement about any one handler. The scope file
// narrows it. An absent scope is not an error, a bundle built before the
// extraction pass produced one simply has no narrowing to apply, and the
// handler sees the whole application as it did before.
func LoadScope(handlerID string) (map[string]bool, bool) {
	if handlerID == "" {
		return nil, false
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(linksPath()), ScopeFilename))
	if err != nil {
		return nil, false
	}

	var byHandler map[string][]string
	if err := json.Unmarshal(raw, &byHandler); err != nil {
		return nil, false
	}

	names, ok := byHandler[handlerID]
	if !ok {
		return nil, false
	}

	scope := make(map[string]bool, len(names))
	for _, name := range names {
		scope[name] = true
	}
	return scope, true
}
