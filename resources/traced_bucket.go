package resources

import (
	"context"
	"io"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

type tracedBucket struct {
	inner bucket.Store
	ref   Ref
}

func (b tracedBucket) Get(
	ctx context.Context, key string, opts ...bucket.GetOption,
) (*bucket.Object, error) {
	return telemetry.Traced(ctx, "celerity.bucket.get",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.key", key)),
		func(ctx context.Context, span telemetry.Span) (*bucket.Object, error) {
			object, err := b.inner.Get(ctx, key, opts...)
			if object != nil {
				// How much came back, which for a ranged read is the part
				// rather than the whole and is not knowable before the call.
				span.SetAttributes(telemetry.Int64("bucket.content_length", object.ContentLength))
			}
			return object, err
		})
}

func (b tracedBucket) Put(
	ctx context.Context, key string, body io.Reader, opts ...bucket.PutOption,
) (bucket.PutResult, error) {
	return telemetry.Traced(ctx, "celerity.bucket.put",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.key", key)),
		func(ctx context.Context, _ telemetry.Span) (bucket.PutResult, error) {
			return b.inner.Put(ctx, key, body, opts...)
		})
}

func (b tracedBucket) Delete(
	ctx context.Context, key string, opts ...bucket.DeleteOption,
) error {
	return telemetry.TracedCall(ctx, "celerity.bucket.delete",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.key", key)),
		func(ctx context.Context, _ telemetry.Span) error {
			return b.inner.Delete(ctx, key, opts...)
		})
}

func (b tracedBucket) DeleteMany(
	ctx context.Context, refs []bucket.ObjectRef,
) (bucket.DeleteManyResult, error) {
	return telemetry.Traced(ctx, "celerity.bucket.delete_many",
		resourceAttrs(b.ref, "bucket", telemetry.Int("bucket.object_count", len(refs))),
		func(ctx context.Context, span telemetry.Span) (bucket.DeleteManyResult, error) {
			result, err := b.inner.DeleteMany(ctx, refs)
			// A delete of many is taken partially, so the three counts are what
			// say what became of it.
			span.SetAttributes(
				telemetry.Int("bucket.deleted_count", len(result.Deleted)),
				telemetry.Int("bucket.failed_count", len(result.Failed)),
				telemetry.Int("bucket.unsent_count", len(result.Unsent)),
			)
			return result, err
		})
}

func (b tracedBucket) Info(
	ctx context.Context, key string, opts ...bucket.InfoOption,
) (bucket.ObjectInfo, error) {
	return telemetry.Traced(ctx, "celerity.bucket.info",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.key", key)),
		func(ctx context.Context, _ telemetry.Span) (bucket.ObjectInfo, error) {
			return b.inner.Info(ctx, key, opts...)
		})
}

func (b tracedBucket) Exists(
	ctx context.Context, key string, opts ...bucket.ExistsOption,
) (bool, error) {
	return telemetry.Traced(ctx, "celerity.bucket.exists",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.key", key)),
		func(ctx context.Context, span telemetry.Span) (bool, error) {
			held, err := b.inner.Exists(ctx, key, opts...)
			span.SetAttributes(telemetry.Bool("bucket.exists", held))
			return held, err
		})
}

// List is traced as one page, which is what one call reads. Reading a listing
// through with [Objects] produces one span per page, which is what makes a
// listing that took more requests than expected visible.
func (b tracedBucket) List(
	ctx context.Context, prefix string, opts ...bucket.ListOption,
) ([]bucket.ObjectInfo, bucket.Cursor, error) {
	var cursor bucket.Cursor
	objects, err := telemetry.Traced(ctx, "celerity.bucket.list_page",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.prefix", prefix)),
		func(ctx context.Context, span telemetry.Span) ([]bucket.ObjectInfo, error) {
			page, next, err := b.inner.List(ctx, prefix, opts...)
			cursor = next
			span.SetAttributes(
				telemetry.Int("bucket.object_count", len(page)),
				telemetry.Bool("bucket.has_more", next.More()),
			)
			return page, err
		})
	return objects, cursor, err
}

func (b tracedBucket) Versions(
	ctx context.Context, prefix string, opts ...bucket.ListOption,
) ([]bucket.ObjectVersion, bucket.Cursor, error) {
	var cursor bucket.Cursor
	versions, err := telemetry.Traced(ctx, "celerity.bucket.list_versions",
		resourceAttrs(b.ref, "bucket", telemetry.String("bucket.prefix", prefix)),
		func(ctx context.Context, span telemetry.Span) ([]bucket.ObjectVersion, error) {
			page, next, err := b.inner.Versions(ctx, prefix, opts...)
			cursor = next
			span.SetAttributes(
				telemetry.Int("bucket.version_count", len(page)),
				telemetry.Bool("bucket.has_more", next.More()),
			)
			return page, err
		})
	return versions, cursor, err
}

func (b tracedBucket) Copy(
	ctx context.Context, sourceKey string, dest bucket.Destination, opts ...bucket.CopyOption,
) (bucket.CopyResult, error) {
	return telemetry.Traced(ctx, "celerity.bucket.copy",
		resourceAttrs(b.ref, "bucket",
			telemetry.String("bucket.source_key", sourceKey),
			telemetry.String("bucket.dest_key", dest.Key),
			// Whether the copy leaves this bucket, which is what decides
			// whether another resource's permissions are involved.
			telemetry.Bool("bucket.cross_bucket", dest.Store != nil),
		),
		func(ctx context.Context, _ telemetry.Span) (bucket.CopyResult, error) {
			return b.inner.Copy(ctx, sourceKey, dest, opts...)
		})
}

// SignedURL is traced although it makes no request as a signature is computed
// from credentials the process already holds.
func (b tracedBucket) SignedURL(
	ctx context.Context, key string, action bucket.SignAction,
	expiry time.Duration, opts ...bucket.SignOption,
) (bucket.SignedURL, error) {
	return telemetry.Traced(ctx, "celerity.bucket.sign_url",
		resourceAttrs(b.ref, "bucket",
			telemetry.String("bucket.key", key),
			telemetry.String("bucket.sign_operation", string(action)),
		),
		func(ctx context.Context, _ telemetry.Span) (bucket.SignedURL, error) {
			return b.inner.SignedURL(ctx, key, action, expiry, opts...)
		})
}
