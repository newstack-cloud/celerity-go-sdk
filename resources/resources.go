// Package resources holds what every resource kind has in common, including how a
// blueprint resource is named, referred to and resolved, and the provider
// interface a platform module implements.
//
// The vocabulary for working with a resource lives in the package for its kind:
// [github.com/newstack-cloud/celerity-go-sdk/resources/datastore],
// [github.com/newstack-cloud/celerity-go-sdk/resources/bucket] and the rest.
// Implementations are per provider and live in their own modules, such as
// resources/aws, so that an application that never touches S3 does not compile
// the AWS SDK into its binary.
//
// A handle is obtained by naming the blueprint resource:
//
//	uploads := resources.Bucket(app, "uploadsBucket")
//	orders := resources.Datastore(app, "ordersTable")
//
// Those calls are also what the extraction pass reads in order to work out which
// resources each handler reaches, and so which IAM grants it needs, so the name
// is expected to be a compile-time constant.
package resources

// Kind names a resource type, and is the middle segment of a resource
// reference.
type Kind string

const (
	KindBucket      Kind = "bucket"
	KindQueue       Kind = "queue"
	KindTopic       Kind = "topic"
	KindDatastore   Kind = "datastore"
	KindCache       Kind = "cache"
	KindSQLDatabase Kind = "sqlDatabase"
	KindConfig      Kind = "config"
)

// DefaultName is the reference emitted when a handle is taken without a name,
// meaning the only resource of its type. It is resolved against the blueprint
// by the CLI rather than by the SDK, which cannot see the blueprint.
const DefaultName = "default"
