// Package local is the resource provider a `celerity dev` session runs under.
//
// It is selected by importing it, which `celerity-go generate --local` writes
// for a development session and never for a deployment:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/local"
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/local/redis"
//
// # What it serves and what it delegates
//
// A queue and a topic, and nothing else. The other four kinds are handed to
// whichever platform provider is also linked, so a session reaches the
// emulators that provider was pointed at.
//
// Most of what a session runs speaks the protocol the real service speaks:
// MinIO answers S3 requests and DynamoDB Local answers DynamoDB's, so a bucket
// and a data store are reached through the platform's own provider with its
// endpoint pointed elsewhere. SQS and SNS have no such stand-in, so those two
// are served locally instead.
//
// # Where the serving happens
//
// Not here. This package decides which kinds a session serves and resolves the
// rest; what carries a local queue or topic is a backend package, linked
// alongside and registering itself. Today that is resources/local/redis, which
// this package knows nothing about.
package local

import (
	"context"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/local/internal/backend"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Name identifies this provider in errors and in the handler manifest.
const Name = "local"

func init() {
	resources.RegisterProvider(New())
}

// New returns the local resource provider.
//
// An application does not construct one: importing this package registers it.
// Exported for a test that drives the provider directly.
func New() *Provider {
	return &Provider{}
}

// Provider serves the resource kinds a development session has to serve itself,
// and delegates every other kind to the platform's provider.
type Provider struct {
	// platform is what the delegated kinds are handed to. Nil in a real build,
	// where it is whichever other provider is linked; set by a test driving the
	// delegation without a registry to arrange.
	platform resources.Provider
}

// Name identifies the provider.
func (p *Provider) Name() string {
	return Name
}

// Detect reports whether this process is a development session.
//
// The CLI says so explicitly rather than this being inferred: a session's
// deploy target is still the platform the application will be deployed to, so
// the platform's own provider is linked alongside. Exactly one of the two
// detects at a time, whichever order the generated file imports them in.
func (p *Provider) Detect() bool {
	return config.CurrentPlatform() == config.PlatformLocal
}

// Queue returns a handle to whatever the session runs in place of a queue.
func (p *Provider) Queue(ref resources.Ref) (queue.Client, error) {
	build, err := backend.Queues()
	if err != nil {
		return nil, err
	}
	return build(ref)
}

// Topic returns a handle to whatever the session runs in place of a topic.
func (p *Provider) Topic(ref resources.Ref) (topic.Client, error) {
	build, err := backend.Topics()
	if err != nil {
		return nil, err
	}
	return build(ref)
}

// Bucket hands a bucket to the platform's provider, which a session reaches
// through the endpoint it was pointed at.
func (p *Provider) Bucket(ref resources.Ref) (bucket.Store, error) {
	return delegate(p, ref, resources.Provider.Bucket)
}

// Cache hands a cache to the platform's provider. A cache is the same code on
// every platform, including the Valkey a session runs, so there is nothing to
// redirect.
func (p *Provider) Cache(ref resources.Ref) (cache.Client, error) {
	return delegate(p, ref, resources.Provider.Cache)
}

// Datastore hands a data store to the platform's provider, which a session
// reaches through the endpoint it was pointed at.
func (p *Provider) Datastore(ref resources.Ref) (datastore.Client, error) {
	return delegate(p, ref, resources.Provider.Datastore)
}

// SQLDatabase hands a database to the platform's provider, which a session
// points at whatever it runs.
func (p *Provider) SQLDatabase(ref resources.Ref) (sqldb.Client, error) {
	return delegate(p, ref, resources.Provider.SQLDatabase)
}

// Close gives back what the linked backends hold, which for a queue and a topic
// over Redis is the connection they share.
//
// The platform provider's own release is not called from here: both are
// registered, so the application releases each of them.
func (p *Provider) Close(ctx context.Context) error {
	return backend.Release(ctx)
}

// delegate hands one resource kind to the platform provider.
//
// Generic over what the kind builds so that the four methods are four lines
// rather than four bodies, and so that a kind this package forgets to delegate
// is a compile error rather than a nil client.
func delegate[T any](
	p *Provider, ref resources.Ref, build func(resources.Provider, resources.Ref) (T, error),
) (T, error) {
	var zero T

	platform, ok := p.fallback()
	if !ok {
		return zero, fmt.Errorf(
			"celerity: a development session serves a queue and a topic itself and leaves "+
				"%s to the platform's provider, but none is linked. The generated platform "+
				"file should import one alongside this package", ref)
	}
	return build(platform, ref)
}

// fallback is the provider the delegated kinds go to.
func (p *Provider) fallback() (resources.Provider, bool) {
	if p.platform != nil {
		return p.platform, true
	}
	return resources.FallbackProvider(Name)
}
