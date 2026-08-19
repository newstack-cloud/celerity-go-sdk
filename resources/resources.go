// Package resources holds the provider-agnostic interfaces a handler reaches
// infrastructure through: buckets, queues, topics, data stores, caches and SQL
// databases.
//
// Only the interfaces live here. Implementations are per provider and live in
// their own modules, such as resources/aws, so that an application that never
// touches S3 does not compile the AWS SDK into its binary.
//
// A handle is obtained by naming the blueprint resource:
//
//	bucket := resources.Bucket(app, "ordersBucket")
//
// Those calls are also what the extraction pass reads in order to work out which
// resources each handler reaches, and so which IAM grants it needs, so the name
// is expected to be a compile-time constant.
package resources

import (
	"context"
	"io"
	"time"
)

// Kind names a resource type. It is the middle segment of a resource reference
// and matches the vocabulary the other SDKs use in their DI tokens.
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

// BucketStore is object storage: S3, Cloud Storage, Azure Blob Storage.
type BucketStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, body io.Reader, opts ...PutOption) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string, opts ...ListOption) ([]ObjectInfo, error)
	SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// ObjectInfo describes one stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

// PutOption configures a write.
type PutOption func(*PutOptions)

// PutOptions is the resolved configuration for a write.
type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

// ListOption configures a listing.
type ListOption func(*ListOptions)

// ListOptions is the resolved configuration for a listing.
type ListOptions struct {
	Limit int
	After string
}

// QueueClient is a point-to-point queue: SQS, Cloud Tasks, Service Bus.
type QueueClient interface {
	Send(ctx context.Context, body []byte, opts ...SendOption) (string, error)
	SendBatch(ctx context.Context, bodies [][]byte, opts ...SendOption) ([]string, error)
}

// SendOption configures a queue or topic send.
type SendOption func(*SendOptions)

// SendOptions is the resolved configuration for a send.
type SendOptions struct {
	Delay      time.Duration
	Attributes map[string]string
	// GroupID orders messages within a group on queues that support it.
	GroupID string
	// DeduplicationID suppresses a duplicate send within the provider's window.
	DeduplicationID string
}

// TopicClient is publish-subscribe: SNS, Pub/Sub, Service Bus Topics.
type TopicClient interface {
	Publish(ctx context.Context, body []byte, opts ...SendOption) (string, error)
}

// CacheClient is a key-value cache: ElastiCache, Memorystore, Azure Cache.
type CacheClient interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
	Increment(ctx context.Context, key string, delta int64) (int64, error)
}

// DatastoreClient is a NoSQL document store: DynamoDB, Firestore, Cosmos DB.
type DatastoreClient interface {
	Get(ctx context.Context, key Key, out any) error
	Put(ctx context.Context, key Key, item any) error
	Delete(ctx context.Context, key Key) error
	Query(ctx context.Context, q Query, out any) error
}

// Key addresses one item in a data store.
type Key struct {
	Partition string
	Sort      string
}

// Query describes a data store query.
type Query struct {
	Partition string
	Index     string
	Limit     int
	StartFrom Key
}

// SQLDatabaseClient is a relational database, handing out separate writer and
// reader endpoints so that a read replica is used where one exists.
type SQLDatabaseClient interface {
	Writer(ctx context.Context) (Conn, error)
	Reader(ctx context.Context) (Conn, error)
}

// Conn is the subset of database/sql a handler needs, kept as an interface so
// that a driver is never forced on a caller.
type Conn interface {
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	Close() error
}

// Result reports the effect of a statement.
type Result interface {
	RowsAffected() (int64, error)
	LastInsertId() (int64, error)
}

// Rows iterates a result set.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}
