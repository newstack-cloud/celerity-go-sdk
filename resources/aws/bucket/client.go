package bucket

import (
	"context"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/service"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

// Linking this package is what lets an application reach a bucket on AWS.
func init() {
	service.RegisterBucket(func(s *service.Session) service.Builder[bucket.Store] {
		return newBuckets(s).build
	})
}

// API is what this package calls on S3. This is narrow on purpose,
// it is also the list of what a handler reaching a bucket needs permission to do.
type API interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CopyObject(context.Context, *s3.CopyObjectInput, ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	ListObjectVersions(
		context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options),
	) (*s3.ListObjectVersionsOutput, error)

	// The rest are what the transfer manager needs, which is what writes an
	// object: a body small enough goes as one PutObject and a larger one is cut
	// into parts.
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	CreateMultipartUpload(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	UploadPart(context.Context, *s3.UploadPartInput, ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(context.Context, *s3.CompleteMultipartUploadInput, ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(context.Context, *s3.AbortMultipartUploadInput, ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
}

// PresignAPI is presigning, which is a separate client rather than a call and so
// a separate seam.
//
// Presigning doesn't make any requests, a signature is computed from the credentials the
// process already holds, so neither of these is a permission a handler needs.
// What the URL is used for is, but that is the holder of the URL doing it.
type PresignAPI interface {
	PresignGetObject(
		context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions),
	) (*v4.PresignedHTTPRequest, error)
	PresignPutObject(
		context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions),
	) (*v4.PresignedHTTPRequest, error)
}

// buckets builds bucket handles for one provider, sharing the S3 clients per
// region.
type buckets struct {
	session  *service.Session
	clients  service.Clients[API]
	presigns service.Clients[PresignAPI]

	// Stood in for by tests, and nil everywhere else.
	api        API
	presignAPI PresignAPI
}

func newBuckets(s *service.Session) *buckets { return &buckets{session: s} }

func (b *buckets) build(ref resources.Ref) (bucket.Store, error) {
	return &s3Bucket{buckets: b, ref: ref}, nil
}

func (b *buckets) s3(ctx context.Context, key service.ClientKey) (API, error) {
	if b.api != nil {
		return b.api, nil
	}

	return b.clients.Get(ctx, key, func() (API, error) {
		cfg, err := b.session.ConfigFor(ctx, key.Region)
		if err != nil {
			return nil, err
		}
		return s3.NewFromConfig(cfg, pathStyleForEmulators(cfg)), nil
	})
}

func (b *buckets) s3Presign(ctx context.Context, key service.ClientKey) (PresignAPI, error) {
	if b.presignAPI != nil {
		return b.presignAPI, nil
	}

	return b.presigns.Get(ctx, key, func() (PresignAPI, error) {
		cfg, err := b.session.ConfigFor(ctx, key.Region)
		if err != nil {
			return nil, err
		}
		return s3.NewPresignClient(s3.NewFromConfig(cfg, pathStyleForEmulators(cfg))), nil
	})
}

// S3EndpointEnvVar overrides the endpoint for S3 alone, which is how a
// development session points at a local emulator while everything else stays on AWS.
const S3EndpointEnvVar = "AWS_ENDPOINT_URL_S3"

// pathStyleForEmulators addresses a bucket as a path rather than as a subdomain
// where an endpoint has been configured.
//
// S3 itself wants the subdomain form, and it is what the client does by
// default. An endpoint is only ever set to reach something standing in for S3,
// such as an emulator (e.g. LocalStack or MinIO) in a development session or test,
// where neither has DNS for a subdomain per bucket.
func pathStyleForEmulators(cfg aws.Config) func(*s3.Options) {
	configured := cfg.BaseEndpoint != nil || os.Getenv(S3EndpointEnvVar) != ""
	return func(o *s3.Options) {
		o.UsePathStyle = configured
	}
}
