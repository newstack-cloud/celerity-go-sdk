// The bucket package is the provider-agnostic interface to object storage: S3,
// Cloud Storage, Azure Blob Storage.
//
// A handle is taken by naming the blueprint resource:
//
//	uploads := resources.Bucket(app, "uploadsBucket")
//
// Implementations are per provider and live in their own modules, such as
// resources/aws.
package bucket

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound reports that the bucket holds no object under the key asked for.
//
//	if errors.Is(err, bucket.ErrNotFound) { ... }
//
// Not used by a delete, which is idempotent: a key that holds nothing is not a
// failure.
var ErrNotFound = errors.New("celerity: not found")

// Store is a bucket/container in an object storage service (e.g. S3, Cloud Storage, Azure Blob Storage).
type Store interface {
	// Get returns an object's content and what the store knows about it.
	// The body needs to be closed on read.
	Get(ctx context.Context, key string, opts ...GetOption) (*Object, error)
	// Put stores an object, and reports what the store recorded about it.
	Put(ctx context.Context, key string, body io.Reader, opts ...PutOption) (PutResult, error)
	// Delete removes an object, and is idempotent. A key that holds nothing
	// does not produce an error.
	//
	// Deletes the current version where the bucket has versioning on, which
	// hides the versions rather than removing them. Naming a version in
	// [DeleteOptions] removes that one for good.
	Delete(ctx context.Context, key string, opts ...DeleteOption) error
	// DeleteMany removes several objects in as few requests as the store
	// allows. A delete of many is taken partially, so every reference comes
	// back in exactly one of the three lists of [DeleteManyResult].
	DeleteMany(ctx context.Context, refs []ObjectRef) (DeleteManyResult, error)
	// Info returns what the store knows about an object without fetching it.
	//
	// Reports [ErrNotFound] where the key doesn't hold anything.
	Info(ctx context.Context, key string, opts ...InfoOption) (ObjectInfo, error)
	// Exists reports whether the store holds an object under the key. A key
	// that holds nothing produces false rather than [ErrNotFound].
	//
	// Without a version this asks about the current one, which on a versioned
	// bucket is false where the newest version is a deletion marker, even
	// though every version under it is still there. Naming a version asks
	// about that version, which can be there while the current one is not.
	Exists(ctx context.Context, key string, opts ...ExistsOption) (bool, error)
	// List returns one page of objects under a prefix, and the cursor to
	// resume from. Use [Objects] to read a listing to the end.
	//
	// Lists the current version of each key. Use [Store.Versions] to see what
	// else a versioned bucket holds.
	List(ctx context.Context, prefix string, opts ...ListOption) ([]ObjectInfo, Cursor, error)
	// Versions returns one page of the versions of the objects under a prefix,
	// newest first per key, and the cursor to resume from. Use [AllVersions] to
	// read it to the end.
	//
	// Every version of every matching key, including the deletion markers a
	// delete left behind, so a restore reads the newest version that is not a
	// marker. A bucket without versioning answers with one version per key.
	Versions(ctx context.Context, prefix string, opts ...ListOption) ([]ObjectVersion, Cursor, error)
	// Copy an object, within this bucket or into another, and report
	// what the store recorded about the copy.
	//
	// The store does the copying, so the content does not pass through the
	// handler and large objects can be copied without loading the contents
	// at runtime for an application.
	Copy(
		ctx context.Context, sourceKey string, dest Destination, opts ...CopyOption,
	) (CopyResult, error)
	// SignedURL returns a URL granting access to one object until it expires,
	// so a browser can read or write it without the bytes passing through a
	// handler and without credentials of its own.
	SignedURL(
		ctx context.Context, key string, action SignAction, expiry time.Duration, opts ...SignOption,
	) (SignedURL, error)
}

// Object is a stored object, including its content, and what the store knows about it.
type Object struct {
	// Body is the content, which has to be closed.
	//
	// Where a [Range] was asked for this is that part of the object rather than
	// the whole of it.
	Body io.ReadCloser
	// Info is what the store knows about the object. Size is the whole object's
	// even where a range was read, since that is what the store reports.
	Info ObjectInfo
	// ContentLength is how many bytes Body yields, which for a ranged read is
	// the part rather than the whole. Equal to Info.Size for a read of the
	// whole object.
	ContentLength int64
}

// ObjectInfo describes one stored object.
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	// ContentType is the type the object was stored with, where the store
	// recorded one.
	//
	// This is empty in a listing, a store answers a listing with the keys and their
	// sizes rather than with each object's headers, so reading this needs
	// [Store.Info] or [Store.Get].
	ContentType string
	// Metadata is what was stored alongside the object by [PutOptions].
	//
	// Empty in a listing, for the same reason as ContentType.
	Metadata map[string]string
	// VersionID names the version this describes, where the bucket has
	// versioning on. Empty where it does not, and empty in a listing. A listing
	// reports the current version of each key, so there is no version to name.
	VersionID string
}

// ObjectRef names one object, and optionally one version of it.
type ObjectRef struct {
	// Key is the object's key.
	Key string
	// VersionID names one version. Empty means the current one.
	VersionID string
}

// ObjectVersion is one version of a stored object.
type ObjectVersion struct {
	Key          string
	VersionID    string
	Size         int64
	LastModified time.Time
	ETag         string
	// Current reports whether this is the version a read without a version
	// would return.
	Current bool
	// DeleteMarker reports a version that records a deletion rather than
	// content. There is nothing to read, this is what makes the versions under it
	// stop being found, and removing it is what brings them back.
	DeleteMarker bool
}

// GetOption configures a read.
type GetOption func(*GetOptions)

// GetOptions is the resolved configuration for a read.
type GetOptions struct {
	// Range reads part of an object rather than the whole of it. The zero Range
	// reads the whole of it.
	Range Range
	// VersionID reads one version rather than the current one. Empty reads the
	// current one.
	VersionID string
}

// InfoOption configures a metadata read.
type InfoOption func(*InfoOptions)

// InfoOptions is the resolved configuration for a metadata read.
type InfoOptions struct {
	// VersionID reads one version's metadata rather than the current one's.
	VersionID string
}

// ExistsOption configures a presence check.
type ExistsOption func(*ExistsOptions)

// ExistsOptions is the resolved configuration for a presence check.
type ExistsOptions struct {
	// VersionID asks whether one version is there rather than whether the key
	// holds a current version.
	VersionID string
}

// DeleteOption configures a delete.
type DeleteOption func(*DeleteOptions)

// DeleteOptions is the resolved configuration for a delete.
type DeleteOptions struct {
	// VersionID removes one version for good rather than hiding the current one
	// behind a marker. Empty deletes the current version.
	VersionID string
}

// PutResult is what the store recorded about an object it stored.
type PutResult struct {
	// ETag identifies the content, and is what a conditional request elsewhere
	// compares against. Its format is the store's own: compare one with another
	// from the same store and never across two.
	ETag string
	// VersionID names the version this write created, where the bucket has
	// versioning on. Empty where it does not.
	VersionID string
}

// CopyResult is what the store recorded about the object a copy created.
type CopyResult struct {
	ETag string
	// VersionID names the version the copy created in the destination.
	VersionID string
}

// DeleteManyResult reports what became of each object of a multiple delete.
//
// Every reference handed over appears in exactly one of the three lists.
type DeleteManyResult struct {
	// Deleted is what the store removed.
	Deleted []ObjectRef
	// Failed is what the store refused.
	Failed []DeleteFailure
	// Unsent is what the store was never able to answer for.
	Unsent []DeleteUnsent
}

// DeleteFailure is an object the store refused to remove.
type DeleteFailure struct {
	Ref ObjectRef
	// Code is the store's own name for the refusal.
	Code string
	// Message describes the refusal.
	Message string
}

// DeleteUnsent is an object the store was never able to answer for, because the
// request carrying it did not complete or because an earlier one did not and
// the rest were not attempted.
type DeleteUnsent struct {
	Ref ObjectRef
	// Err is what stopped the delete, and is the same failure for every unsent
	// reference.
	Err error
}

// Range is the part of an object to read.
//
// Measured as a start and a length rather than as two positions, so the zero
// value means the whole object.
type Range struct {
	// Start is the first byte to read, counted from zero.
	Start int64
	// Length is how many bytes to read. Zero reads to the end of the object.
	Length int64
}

// Whole reports whether this is the zero Range, which is the whole object.
func (r Range) Whole() bool {
	return r.Start == 0 && r.Length == 0
}

// PutOption configures a write.
type PutOption func(*PutOptions)

// PutOptions is the resolved configuration for a write.
type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

// Destination is where a copy puts the object.
type Destination struct {
	// Key is what the copy is stored under.
	Key string
	// Store is another bucket to copy into, taken the same way this one was.
	// Nil copies within the same bucket.
	//
	// Both buckets have to be the same provider's, since the store does the
	// copying and one service cannot reach into another's.
	Store Store
}

// CopyOption configures a copy.
type CopyOption func(*CopyOptions)

// CopyOptions is the resolved configuration for a copy.
//
// The copy carries the source's content type and metadata where neither is
// given here. Giving either replaces both, the two travel together in the
// stores, so one cannot be overridden while the other is inherited.
type CopyOptions struct {
	ContentType string
	Metadata    map[string]string
}

// Replaces reports whether these options replace the source's content type and
// metadata rather than carrying them over.
func (o CopyOptions) Replaces() bool {
	return o.ContentType != "" || len(o.Metadata) > 0
}

// SignAction is what a signed URL grants.
type SignAction string

const (
	// SignRead grants a download.
	SignRead SignAction = "read"
	// SignWrite grants an upload, in a single request.
	SignWrite SignAction = "write"
)

// SignOption configures a signed URL.
type SignOption func(*SignOptions)

// SignedURL is a URL granting temporary access to one object, and the moment it
// stops working.
type SignedURL struct {
	// URL is the signed URL.
	URL string
	// ExpiresAt is when it stops working.
	//
	// Computed from the expiry asked for. A URL signed with credentials that
	// expire sooner stops working sooner, which on a serverless platform is the
	// usual case.
	ExpiresAt time.Time
}

// SignOptions is the resolved configuration for a signed URL.
type SignOptions struct {
	// ContentType is the type an upload has to declare.
	//
	// Signed into the URL where the store supports it, so an upload declaring
	// anything else is refused rather than stored under a type nobody expected.
	// Ignored by [SignRead], which has nothing to constrain.
	ContentType string
}

// ListOption configures a listing.
type ListOption func(*ListOptions)

// ListOptions is the resolved configuration for a listing.
type ListOptions struct {
	// Limit is the most objects one page holds. Zero leaves it to the store.
	Limit int
	// Cursor resumes a listing where an earlier one stopped. See [Cursor].
	Cursor string
}

// Cursor is a position in a listing, to resume it from.
//
// A cursor is opaque what is in one belongs to the store that produced it,
// and only that store can read it back. Empty means a listing reached its end.
type Cursor string

// More reports whether a listing has more to give.
func (c Cursor) More() bool {
	return c != ""
}
