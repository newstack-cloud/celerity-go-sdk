package bucket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

// The whole of the interface is implemented here, across the files of this
// package.
//
// This makes sure that at compile time, the S3 implementation doesn't break
// the contract.
var _ bucket.Store = (*s3Bucket)(nil)

type s3Bucket struct {
	buckets *buckets
	ref     resources.Ref
}

func (b *s3Bucket) Get(
	ctx context.Context, key string, opts ...bucket.GetOption,
) (*bucket.Object, error) {
	client, name, err := b.resolve(ctx)
	if err != nil {
		return nil, err
	}

	var options bucket.GetOptions
	for _, opt := range opts {
		opt(&options)
	}

	in := &s3.GetObjectInput{Bucket: aws.String(name), Key: aws.String(key)}
	if header := rangeHeader(options.Range); header != "" {
		in.Range = aws.String(header)
	}
	if options.VersionID != "" {
		in.VersionId = aws.String(options.VersionID)
	}

	out, err := client.GetObject(ctx, in)
	if err != nil {
		return nil, b.wrapRead("reading", key, err)
	}

	return &bucket.Object{
		Body: out.Body,
		Info: bucket.ObjectInfo{
			Key:          key,
			Size:         objectSize(out),
			LastModified: aws.ToTime(out.LastModified),
			ETag:         aws.ToString(out.ETag),
			ContentType:  aws.ToString(out.ContentType),
			Metadata:     out.Metadata,
			VersionID:    aws.ToString(out.VersionId),
		},
		ContentLength: aws.ToInt64(out.ContentLength),
	}, nil
}

// Extracts the whole object's size rather than the part that was read.
//
// A ranged read answers with the length of the part, and with the whole in
// Content-Range as "bytes start-end/total". Reporting the part would make Size
// mean two different things depending on how the object was asked for; the part
// is Object.ContentLength.
func objectSize(out *s3.GetObjectOutput) int64 {
	if total, ok := totalFromContentRange(aws.ToString(out.ContentRange)); ok {
		return total
	}
	return aws.ToInt64(out.ContentLength)
}

func totalFromContentRange(header string) (int64, bool) {
	_, total, found := strings.Cut(header, "/")
	if !found {
		return 0, false
	}
	size, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return 0, false
	}
	return size, true
}

// The range as HTTP states one, whose positions are the first
// and last bytes wanted and include both.
func rangeHeader(r bucket.Range) string {
	if r.Whole() {
		return ""
	}
	if r.Length <= 0 {
		return fmt.Sprintf("bytes=%d-", r.Start)
	}
	return fmt.Sprintf("bytes=%d-%d", r.Start, r.Start+r.Length-1)
}

// Put stores an object, cutting a large body into parts.
//
// The body is an io.Reader rather than a []byte, so a handler streaming a large
// object never holds the full object. The transfer manager is what makes that possible, a
// single PutObject has to know the length up front, which a stream cannot say,
// and S3 refuses one over five gigabytes whatever the length is.
func (b *s3Bucket) Put(
	ctx context.Context, key string, body io.Reader, opts ...bucket.PutOption,
) (bucket.PutResult, error) {
	client, name, err := b.resolve(ctx)
	if err != nil {
		return bucket.PutResult{}, err
	}

	var options bucket.PutOptions
	for _, opt := range opts {
		opt(&options)
	}

	in := &transfermanager.UploadObjectInput{
		Bucket: aws.String(name),
		Key:    aws.String(key),
		Body:   body,
	}
	if options.ContentType != "" {
		in.ContentType = aws.String(options.ContentType)
	}
	if len(options.Metadata) > 0 {
		in.Metadata = options.Metadata
	}

	out, err := transfermanager.New(client).UploadObject(ctx, in)
	if err != nil {
		return bucket.PutResult{}, b.wrap("writing", key, err)
	}
	return bucket.PutResult{
		ETag:      aws.ToString(out.ETag),
		VersionID: aws.ToString(out.VersionID),
	}, nil
}

// Delete removes an object, and is idempotent: S3 answers a delete of a key
// that is not there the same way it answers one that was.
func (b *s3Bucket) Delete(
	ctx context.Context, key string, opts ...bucket.DeleteOption,
) error {
	client, name, err := b.resolve(ctx)
	if err != nil {
		return err
	}

	var options bucket.DeleteOptions
	for _, opt := range opts {
		opt(&options)
	}

	in := &s3.DeleteObjectInput{
		Bucket: aws.String(name),
		Key:    aws.String(key),
	}
	if options.VersionID != "" {
		in.VersionId = aws.String(options.VersionID)
	}

	if _, err = client.DeleteObject(ctx, in); err != nil {
		return b.wrap("deleting", key, err)
	}
	return nil
}

// Info returns what S3 knows about an object without fetching it.
func (b *s3Bucket) Info(
	ctx context.Context, key string, opts ...bucket.InfoOption,
) (bucket.ObjectInfo, error) {
	client, name, err := b.resolve(ctx)
	if err != nil {
		return bucket.ObjectInfo{}, err
	}

	var options bucket.InfoOptions
	for _, opt := range opts {
		opt(&options)
	}

	in := &s3.HeadObjectInput{Bucket: aws.String(name), Key: aws.String(key)}
	if options.VersionID != "" {
		in.VersionId = aws.String(options.VersionID)
	}

	out, err := client.HeadObject(ctx, in)
	if err != nil {
		return bucket.ObjectInfo{}, b.wrapRead(
			"reading the metadata of", key, err,
		)
	}

	return bucket.ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		LastModified: aws.ToTime(out.LastModified),
		ETag:         aws.ToString(out.ETag),
		ContentType:  aws.ToString(out.ContentType),
		Metadata:     out.Metadata,
		VersionID:    aws.ToString(out.VersionId),
	}, nil
}

// Exists reports whether the bucket holds an object under the key.
//
// A HeadObject, the same request Info makes: S3 has no cheaper way to ask, and
// the answer is the presence of what the metadata describes.
func (b *s3Bucket) Exists(
	ctx context.Context, key string, opts ...bucket.ExistsOption,
) (bool, error) {
	var options bucket.ExistsOptions
	for _, opt := range opts {
		opt(&options)
	}

	var info []bucket.InfoOption
	if options.VersionID != "" {
		info = append(info, func(o *bucket.InfoOptions) {
			o.VersionID = options.VersionID
		})
	}

	_, err := b.Info(ctx, key, info...)
	if errors.Is(err, bucket.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Copy an object, within this bucket or into another.
//
// One request to S3, which does the copying itself, so the content never passes
// through the handler regardless of its size.
func (b *s3Bucket) Copy(
	ctx context.Context, sourceKey string, dest bucket.Destination, opts ...bucket.CopyOption,
) (bucket.CopyResult, error) {
	name, err := b.ref.ID(ctx)
	if err != nil {
		return bucket.CopyResult{}, err
	}

	destName, destRegion := name, ""
	if dest.Store != nil {
		if destName, destRegion, err = destinationOf(ctx, dest.Store); err != nil {
			return bucket.CopyResult{}, fmt.Errorf(
				"celerity: copying %s %q: %w", b.ref, sourceKey, err)
		}
	} else if destRegion, err = b.region(ctx); err != nil {
		return bucket.CopyResult{}, err
	}

	// The destination's region rather than this bucket's: S3 routes a copy to
	// the bucket being written to, and answers a request sent anywhere else
	// with a redirect rather than copying. The source may be in another region
	// and S3 reads it from there.
	client, err := b.buckets.s3(ctx, service.ClientKey{Region: destRegion})
	if err != nil {
		return bucket.CopyResult{}, err
	}

	var options bucket.CopyOptions
	for _, opt := range opts {
		opt(&options)
	}

	in := &s3.CopyObjectInput{
		Bucket: aws.String(destName),
		Key:    aws.String(dest.Key),
		// The source is a path rather than two fields, and has to be escaped:
		// a key holding a question mark or a hash would otherwise be read as
		// the start of a query or a fragment.
		CopySource: aws.String(url.PathEscape(name + "/" + sourceKey)),
	}
	if options.Replaces() {
		in.MetadataDirective = s3types.MetadataDirectiveReplace
		in.Metadata = options.Metadata
		if options.ContentType != "" {
			in.ContentType = aws.String(options.ContentType)
		}
	}

	out, err := client.CopyObject(ctx, in)
	if err != nil {
		return bucket.CopyResult{}, b.wrap("copying", sourceKey, err)
	}

	var etag string
	if out.CopyObjectResult != nil {
		etag = aws.ToString(out.CopyObjectResult.ETag)
	}
	return bucket.CopyResult{
		ETag:      etag,
		VersionID: aws.ToString(out.VersionId),
	}, nil
}

// Resolves the bucket another handle represents, and the region it is in.
//
// A copy is one request to one service, so a destination this package cannot
// resolve is not a copy it can make. Said plainly rather than left as a type
// assertion failure, since the likely cause is a handle from another provider.
func destinationOf(ctx context.Context, store bucket.Store) (name, region string, err error) {
	other, ok := store.(*s3Bucket)
	if !ok {
		return "", "", fmt.Errorf(
			"the destination is not a bucket on AWS: S3 copies within itself, so both "+
				"buckets have to be S3's, and this one is a %T", store)
	}
	if name, err = other.ref.ID(ctx); err != nil {
		return "", "", err
	}
	region, err = other.region(ctx)
	return name, region, err
}

// List returns one page of the objects under a prefix, and the cursor to
// resume from.
//
// One page rather than the whole prefix, for the same reason a query returns
// one. A handler paging for a caller needs somewhere to stop and something to
// hand back, and a handler that wants the lot can use [bucket.Objects] to read
// it through.
func (b *s3Bucket) List(
	ctx context.Context, prefix string, opts ...bucket.ListOption,
) ([]bucket.ObjectInfo, bucket.Cursor, error) {
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

	in := &s3.ListObjectsV2Input{Bucket: aws.String(name)}
	if prefix != "" {
		in.Prefix = aws.String(prefix)
	}
	if after != "" {
		in.StartAfter = aws.String(after)
	}
	if options.Limit > 0 {
		in.MaxKeys = aws.Int32(int32(min(options.Limit, maxKeysPerPage)))
	}

	out, err := client.ListObjectsV2(ctx, in)
	if err != nil {
		return nil, "", b.wrap("listing", prefix, err)
	}

	found := objectsOf(out.Contents)
	if !aws.ToBool(out.IsTruncated) || len(found) == 0 {
		return found, "", nil
	}

	// The last key seen rather than the continuation token S3 offers, so that
	// a listing resumes from where the caller actually read to. A page handed
	// out whole is read whole, but the two are only the same while that holds,
	// and the key is the thing that is true either way.
	return found, encodeKeyAfter(found[len(found)-1].Key), nil
}

// maxKeysPerPage is S3's own ceiling on one listing request. Asking for more is
// not refused, it is silently one page of this size, so the paging below has to
// assume it whatever was asked for.
const maxKeysPerPage = 1000

// Converts a page of a listing.
//
// ContentType and Metadata are left empty, a listing answers with the keys and
// their sizes rather than with each object's headers, and a request per object
// to fill them in is not something a listing should do, to minimise the amount
// of requests, that is something the caller should do for only the relevant
// objects from the listing.
func objectsOf(contents []s3types.Object) []bucket.ObjectInfo {
	out := make([]bucket.ObjectInfo, 0, len(contents))
	for _, object := range contents {
		out = append(out, bucket.ObjectInfo{
			Key:          aws.ToString(object.Key),
			Size:         aws.ToInt64(object.Size),
			LastModified: aws.ToTime(object.LastModified),
			ETag:         aws.ToString(object.ETag),
		})
	}
	return out
}

// SignedURL returns a URL granting access to one object until it expires.
//
// Signed with the function's own credentials, so the URL cannot outlive them.
// On Lambda those are the execution role's session credentials, which is a
// shorter life than a long expiry would suggest.
func (b *s3Bucket) SignedURL(
	ctx context.Context,
	key string,
	action bucket.SignAction,
	expiry time.Duration,
	opts ...bucket.SignOption,
) (bucket.SignedURL, error) {
	name, err := b.ref.ID(ctx)
	if err != nil {
		return bucket.SignedURL{}, err
	}
	clientKey, err := service.KeyFor(ctx, b.ref)
	if err != nil {
		return bucket.SignedURL{}, err
	}
	client, err := b.buckets.s3Presign(ctx, clientKey)
	if err != nil {
		return bucket.SignedURL{}, err
	}

	var options bucket.SignOptions
	for _, opt := range opts {
		opt(&options)
	}

	signed, err := b.sign(ctx, client, name, key, action, expiry, options)
	if err != nil {
		return bucket.SignedURL{}, b.wrap("signing a URL for", key, err)
	}

	// Computed here rather than read back from the signature, which carries the
	// expiry as a duration from a timestamp it does not return.
	return bucket.SignedURL{
		URL:       signed.URL,
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

func (b *s3Bucket) sign(
	ctx context.Context,
	client PresignAPI,
	name, key string,
	action bucket.SignAction,
	expiry time.Duration,
	options bucket.SignOptions,
) (*v4.PresignedHTTPRequest, error) {
	expires := s3.WithPresignExpires(expiry)

	switch action {
	case bucket.SignRead:
		return client.PresignGetObject(
			ctx,
			&s3.GetObjectInput{
				Bucket: aws.String(name),
				Key:    aws.String(key),
			},
			expires,
		)
	case bucket.SignWrite:
		in := &s3.PutObjectInput{
			Bucket: aws.String(name),
			Key:    aws.String(key),
		}
		// Signed into the URL, so an upload declaring a different type is
		// refused rather than stored as something nobody expected.
		if options.ContentType != "" {
			in.ContentType = aws.String(options.ContentType)
		}
		return client.PresignPutObject(ctx, in, expires)
	default:
		return nil, fmt.Errorf(
			"%q is not an action a URL can be signed for, which is %q or %q",
			action, bucket.SignRead, bucket.SignWrite)
	}
}

func (b *s3Bucket) resolve(ctx context.Context) (API, string, error) {
	name, err := b.ref.ID(ctx)
	if err != nil {
		return nil, "", err
	}
	key, err := service.KeyFor(ctx, b.ref)
	if err != nil {
		return nil, "", err
	}

	client, err := b.buckets.s3(ctx, key)
	if err != nil {
		return nil, "", err
	}

	return client, name, nil
}

// Where the deployment recorded this bucket, which is empty for one
// it created for this application.
func (b *s3Bucket) region(ctx context.Context) (string, error) {
	return service.RegionOf(ctx, b.ref)
}

// wrap names the resource as the handler asked for it. The blueprint name is
// the only one the developer wrote, so an error carrying only the deployed
// name would point at something they never typed.
func (b *s3Bucket) wrap(action, key string, err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		if absent[api.ErrorCode()] {
			return fmt.Errorf("celerity: %s holds no object %q: %w", b.ref, key, bucket.ErrNotFound)
		}
		if misrouted[api.ErrorCode()] {
			return b.wrapMisrouted(action, key, err)
		}
	}
	return fmt.Errorf("celerity: %s %s %q: %w", action, b.ref, key, err)
}

// Names a request that reached S3 in a region the bucket is not in.
//
// S3 routes by the region the client was built for and refuses a request that
// arrives anywhere else rather than forwarding it, so this is a bucket being
// reached from the wrong place rather than anything about the object. Worth
// naming, because the raw answer is a redirect or a malformed-signature
// complaint and neither reads as "the region is wrong".
//
// Only reachable for a bucket the blueprint declares as external, since one a
// deployment created for this application is in the application's own region.
// An S3 ARN carries no region, so an external bucket records its own, and the
// likely cause of this is that it records none.
func (b *s3Bucket) wrapMisrouted(action, key string, err error) error {
	return fmt.Errorf(
		"celerity: %s is not in the region it is being reached from, so S3 refused to %s %q "+
			"rather than routing the request. A bucket outside this application's own region "+
			"has to record the region it is in, which its ARN cannot supply: %w",
		b.ref, action, key, err)
}

// The codes S3 answers a request sent to the wrong region with. A redirect is
// what a bucket in another region produces; a malformed authorisation header is
// what the same mistake produces once the request is signed, and the message
// carries the region S3 expected.
var misrouted = map[string]bool{
	"PermanentRedirect":            true,
	"AuthorizationHeaderMalformed": true,
}

// The codes S3 reports absence with, which is more than one: a key that holds
// nothing and a version that is not there are different answers from S3 and the
// same answer to a handler, and NotFound is what a HeadObject says rather than
// the NoSuchKey a GetObject says.
var absent = map[string]bool{
	"NoSuchKey":     true,
	"NoSuchVersion": true,
	"NotFound":      true,
}

// wrapRead is wrap for an operation that reads an object or its metadata, which
// has one more way of saying there is nothing there.
//
// A version that names a deletion marker holds no object: the marker is what
// records that the key was deleted. S3 answers a read of one with 405 rather
// than 404, which is a different answer to the same question, so it is reported
// as absence here. Only on a read: a 405 from anything else is not this.
//
// Reachable through the contract rather than hypothetical, since [Store.Versions]
// hands back the markers along with the versions, and their ids are the kind a
// caller passes straight back in.
func (b *s3Bucket) wrapRead(action, key string, err error) error {
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "MethodNotAllowed" {
		return fmt.Errorf("celerity: %s holds no object %q: %w", b.ref, key, bucket.ErrNotFound)
	}
	return b.wrap(action, key, err)
}
