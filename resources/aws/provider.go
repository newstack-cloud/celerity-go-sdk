// Package aws implements the Celerity resource interfaces against AWS.
//
// It is selected by importing it, which is how an application picks a platform
// without naming one anywhere else:
//
//	import _ "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
//
// An init registers the provider, and the generated platform file writes that
// import for an AWS deploy target, so handler code deals only in the
// provider-agnostic interfaces from the resources package and an application
// doesn't need to name a platform at all.
//
// # What a handle resolves to
//
// A handle is taken by the name the blueprint gave a resource, and what that
// resource is actually called is decided when it is created. The pair is held
// by the deployment: the links file says which config key holds a resource, and
// the resources namespace holds the identifier. This package reads both through
// the [resources.Ref] it is given and never learns how either is written.
//
// Nothing is resolved while handles are being taken. A client built here holds
// its Ref and resolves on the first call a handler makes, so registration costs
// no requests and an application declaring a resource it never reaches pays
// nothing for it.
//
// # Separate module
//
// aws-sdk-go-v2 is a large dependency, and on Lambda a cold start cost rather
// than only a disk one, so an application that never touches AWS does not
// compile it in.
package aws

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Name identifies this provider in errors and in the handler manifest.
const Name = "aws"

func init() {
	resources.RegisterProvider(New())
}

// New returns the AWS resource provider.
//
// An application does not construct one: importing this package registers it,
// and it is selected at startup. It is exported for a test that drives the
// provider directly.
func New() *Provider {
	return &Provider{session: service.NewSession()}
}

// Provider builds AWS clients behind the resource interfaces.
//
// It holds no service client of its own. Each resource kind lives in its own
// package under resources/aws and registers what it builds when it is linked,
// so a build carries the AWS clients for the resources the blueprint declares
// and no others. `celerity-go generate` writes the imports.
type Provider struct {
	session *service.Session

	buckets    service.Lazy[service.Builder[bucket.Store]]
	queues     service.Lazy[service.Builder[queue.Client]]
	topics     service.Lazy[service.Builder[topic.Client]]
	caches     service.Lazy[service.Builder[cache.Client]]
	datastores service.Lazy[service.Builder[datastore.Client]]
	databases  service.Lazy[service.Builder[sqldb.Client]]
}

// Name identifies the provider.
func (p *Provider) Name() string {
	return Name
}

// Close gives back what the linked resource packages hold open: the connection
// pools of a relational database, the sockets of a cache.
//
// Called by celerity.Run when a containerised application stops serving.
// Nothing calls it on Lambda, where the execution environment is frozen between
// invocations and torn down rather than shut down.
func (p *Provider) Close(ctx context.Context) error {
	return service.Release(ctx)
}

// Detect reports whether this process is running on AWS.
//
// It only decides anything when more than one provider is linked, which is not
// how an application is normally built: with one linked it is used whatever the
// environment says, including from a developer's machine where none of the
// platform's own variables are set.
func (p *Provider) Detect() bool {
	return config.CurrentPlatform() == config.PlatformAWS
}

// Bucket returns a handle to an S3 bucket.
func (p *Provider) Bucket(ref resources.Ref) (bucket.Store, error) {
	return build(&p.buckets, p.session, ref, service.Buckets)
}

// Queue returns a handle to an SQS queue.
func (p *Provider) Queue(ref resources.Ref) (queue.Client, error) {
	return build(&p.queues, p.session, ref, service.Queues)
}

// Topic returns a handle to an SNS topic.
func (p *Provider) Topic(ref resources.Ref) (topic.Client, error) {
	return build(&p.topics, p.session, ref, service.Topics)
}

// Cache returns a handle to an ElastiCache instance or cluster.
func (p *Provider) Cache(ref resources.Ref) (cache.Client, error) {
	return build(&p.caches, p.session, ref, service.Caches)
}

// SQLDatabase returns a handle to an RDS database.
func (p *Provider) SQLDatabase(ref resources.Ref) (sqldb.Client, error) {
	return build(&p.databases, p.session, ref, service.SQLDatabases)
}

// Datastore returns a handle to a DynamoDB table.
func (p *Provider) Datastore(ref resources.Ref) (datastore.Client, error) {
	return build(&p.datastores, p.session, ref, service.Datastores)
}

// build finds the builder a resource package registered, once per provider, and
// makes a handle with it. Held rather than found per call so that the service
// client a package builds lazily is shared by every handle of that kind.
func build[T any](
	held *service.Lazy[service.Builder[T]],
	session *service.Session,
	ref resources.Ref,
	find func(*service.Session) (service.Builder[T], error),
) (T, error) {
	var zero T
	builder, err := held.Get(func() (service.Builder[T], error) {
		return find(session)
	})
	if err != nil {
		return zero, err
	}
	return builder(ref)
}
