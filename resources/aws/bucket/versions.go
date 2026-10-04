package bucket

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

// Versions returns one page of the versions of the objects under a prefix.
//
// S3 answers with the versions and the deletion markers in two lists, and this
// returns one. A marker is a version of the key as far as a handler is concerned,
// and the thing it most often wants to know is which version is the newest that
// is not marked as deleted.
func (b *s3Bucket) Versions(
	ctx context.Context, prefix string, opts ...bucket.ListOption,
) ([]bucket.ObjectVersion, bucket.Cursor, error) {
	client, name, err := b.resolve(ctx)
	if err != nil {
		return nil, "", err
	}

	var options bucket.ListOptions
	for _, opt := range opts {
		opt(&options)
	}

	after, err := keyAfter(options.Cursor)
	if err != nil {
		return nil, "", err
	}

	in := &s3.ListObjectVersionsInput{Bucket: aws.String(name)}
	if prefix != "" {
		in.Prefix = aws.String(prefix)
	}
	if after != "" {
		in.KeyMarker = aws.String(after)
	}
	if options.Limit > 0 {
		in.MaxKeys = aws.Int32(int32(min(options.Limit, maxKeysPerPage)))
	}

	out, err := client.ListObjectVersions(ctx, in)
	if err != nil {
		return nil, "", b.wrap("listing the versions under", prefix, err)
	}

	found := versionsOf(out)
	if !aws.ToBool(out.IsTruncated) || len(found) == 0 {
		return found, "", nil
	}
	return found, encodeKeyAfter(found[len(found)-1].Key), nil
}

// Merges the two lists S3 answers with, newest first per key.
//
// S3 returns both already ordered by key and then by age, so the merge is one
// pass over the pair rather than a sort: taking whichever side is older next
// keeps that order without assuming it can be recomputed, which it cannot,
// since a marker and a version can share a timestamp.
func versionsOf(out *s3.ListObjectVersionsOutput) []bucket.ObjectVersion {
	merged := make([]bucket.ObjectVersion, 0, len(out.Versions)+len(out.DeleteMarkers))

	version, marker := 0, 0
	for version < len(out.Versions) || marker < len(out.DeleteMarkers) {
		if takeVersionNext(out, version, marker) {
			merged = append(merged, objectVersion(out.Versions[version]))
			version += 1
			continue
		}
		merged = append(merged, deleteMarker(out.DeleteMarkers[marker]))
		marker += 1
	}
	return merged
}

func takeVersionNext(out *s3.ListObjectVersionsOutput, version, marker int) bool {
	if version >= len(out.Versions) {
		return false
	}
	if marker >= len(out.DeleteMarkers) {
		return true
	}

	nextVersion, nextMarker := out.Versions[version], out.DeleteMarkers[marker]
	if key := aws.ToString(nextVersion.Key); key != aws.ToString(nextMarker.Key) {
		return key < aws.ToString(nextMarker.Key)
	}
	// Within one key the newer comes first, which is the order S3 lists each
	// side in, so the later timestamp wins.
	return aws.ToTime(nextVersion.LastModified).After(aws.ToTime(nextMarker.LastModified))
}

func objectVersion(v s3types.ObjectVersion) bucket.ObjectVersion {
	return bucket.ObjectVersion{
		Key:          aws.ToString(v.Key),
		VersionID:    aws.ToString(v.VersionId),
		Size:         aws.ToInt64(v.Size),
		LastModified: aws.ToTime(v.LastModified),
		ETag:         aws.ToString(v.ETag),
		Current:      aws.ToBool(v.IsLatest),
	}
}

// deleteMarker is a version with nothing to read.
// It has no size and no etag, because a marker has no content.
func deleteMarker(m s3types.DeleteMarkerEntry) bucket.ObjectVersion {
	return bucket.ObjectVersion{
		Key:          aws.ToString(m.Key),
		VersionID:    aws.ToString(m.VersionId),
		LastModified: aws.ToTime(m.LastModified),
		Current:      aws.ToBool(m.IsLatest),
		DeleteMarker: true,
	}
}

// DeleteMany removes several objects, in as few requests as S3 allows.
//
// Refusals are reported rather than raised, the same way a batch send reports
// them: S3 takes a request partially, and a caller handed back an error for the
// whole thing cannot tell which of their objects are gone.
func (b *s3Bucket) DeleteMany(
	ctx context.Context, refs []bucket.ObjectRef,
) (bucket.DeleteManyResult, error) {
	var result bucket.DeleteManyResult
	if len(refs) == 0 {
		return result, nil
	}

	client, name, err := b.resolve(ctx)
	if err != nil {
		result.Unsent = unsentRefs(refs, err)
		return result, err
	}

	for start := 0; start < len(refs); start += maxDeletesPerRequest {
		end := min(start+maxDeletesPerRequest, len(refs))
		chunk, err := b.deleteChunk(ctx, client, name, refs[start:end])
		if err != nil {
			result.Unsent = unsentRefs(refs[start:], err)
			return result, err
		}
		result.Deleted = append(result.Deleted, chunk.Deleted...)
		result.Failed = append(result.Failed, chunk.Failed...)
	}

	return result, nil
}

// maxDeletesPerRequest is S3's own ceiling on one DeleteObjects request.
const maxDeletesPerRequest = 1000

func (b *s3Bucket) deleteChunk(
	ctx context.Context, client API, name string, refs []bucket.ObjectRef,
) (bucket.DeleteManyResult, error) {
	var result bucket.DeleteManyResult

	objects := make([]s3types.ObjectIdentifier, 0, len(refs))
	for _, ref := range refs {
		object := s3types.ObjectIdentifier{Key: aws.String(ref.Key)}
		if ref.VersionID != "" {
			object.VersionId = aws.String(ref.VersionID)
		}
		objects = append(objects, object)
	}

	out, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(name),
		// Quiet leaves out the objects that were removed, which is the half of
		// the answer this has to report, so it stays off.
		Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(false)},
	})
	if err != nil {
		return result, fmt.Errorf("celerity: deleting from %s: %w", b.ref, err)
	}

	for _, deleted := range out.Deleted {
		result.Deleted = append(result.Deleted, bucket.ObjectRef{
			Key:       aws.ToString(deleted.Key),
			VersionID: aws.ToString(deleted.VersionId),
		})
	}
	for _, refused := range out.Errors {
		result.Failed = append(result.Failed, bucket.DeleteFailure{
			Ref: bucket.ObjectRef{
				Key:       aws.ToString(refused.Key),
				VersionID: aws.ToString(refused.VersionId),
			},
			Code:    aws.ToString(refused.Code),
			Message: aws.ToString(refused.Message),
		})
	}
	return result, nil
}

func unsentRefs(refs []bucket.ObjectRef, err error) []bucket.DeleteUnsent {
	out := make([]bucket.DeleteUnsent, 0, len(refs))
	for _, ref := range refs {
		out = append(out, bucket.DeleteUnsent{Ref: ref, Err: err})
	}
	return out
}
