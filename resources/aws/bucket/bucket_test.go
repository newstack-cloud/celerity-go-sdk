package bucket_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsbucket "github.com/newstack-cloud/celerity-go-sdk/resources/aws/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

type BucketTestSuite struct {
	suite.Suite
}

func TestBucketTestSuite(t *testing.T) {
	suite.Run(t, new(BucketTestSuite))
}

// fakeS3 answers the calls a bucket makes and records what it was asked.
type fakeS3 struct {
	awsbucket.API

	get         *s3.GetObjectInput
	put         *s3.PutObjectInput
	deleted     *s3.DeleteObjectInput
	listed      []*s3.ListObjectsV2Input
	head        *s3.HeadObjectInput
	copied      *s3.CopyObjectInput
	deletedMany []*s3.DeleteObjectsInput
	versions    []*s3.ListObjectVersionsInput

	body  string
	pages []*s3.ListObjectsV2Output
	err   error

	// got and headed stand in for what S3 reports about an object, so that what
	// a read passes on can be told apart from what it invents.
	got    *s3.GetObjectOutput
	headed *s3.HeadObjectOutput

	// versionPages and deleteAnswer stand in for the two calls whose answers
	// this package reshapes rather than passes on.
	versionPages []*s3.ListObjectVersionsOutput
	deleteAnswer func(*s3.DeleteObjectsInput) *s3.DeleteObjectsOutput

	// failDeleteFrom is the delete request, counted from one, that fails and
	// every one after it, so that a delete over the ceiling can fail part way.
	failDeleteFrom int
}

func (f *fakeS3) DeleteObjects(
	_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options),
) (*s3.DeleteObjectsOutput, error) {
	f.deletedMany = append(f.deletedMany, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.failDeleteFrom > 0 && len(f.deletedMany) >= f.failDeleteFrom {
		return nil, errors.New("SlowDown")
	}
	if f.deleteAnswer != nil {
		return f.deleteAnswer(in), nil
	}
	return acceptEveryDelete(in), nil
}

// acceptEveryDelete answers the way S3 does when every object was removed.
func acceptEveryDelete(in *s3.DeleteObjectsInput) *s3.DeleteObjectsOutput {
	out := &s3.DeleteObjectsOutput{}
	for _, object := range in.Delete.Objects {
		out.Deleted = append(out.Deleted, s3types.DeletedObject{
			Key: object.Key, VersionId: object.VersionId,
		})
	}
	return out
}

func (f *fakeS3) ListObjectVersions(
	_ context.Context, in *s3.ListObjectVersionsInput, _ ...func(*s3.Options),
) (*s3.ListObjectVersionsOutput, error) {
	f.versions = append(f.versions, in)
	if f.err != nil {
		return nil, f.err
	}
	return f.versionPages[min(len(f.versions)-1, len(f.versionPages)-1)], nil
}

func (f *fakeS3) GetObject(
	_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	f.get = in
	if f.err != nil {
		return nil, f.err
	}
	out := f.got
	if out == nil {
		out = &s3.GetObjectOutput{}
	}
	out.Body = io.NopCloser(strings.NewReader(f.body))
	return out, nil
}

func (f *fakeS3) HeadObject(
	_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	f.head = in
	if f.err != nil {
		return nil, f.err
	}
	if f.headed != nil {
		return f.headed, nil
	}
	return &s3.HeadObjectOutput{}, nil
}

func (f *fakeS3) CopyObject(
	_ context.Context, in *s3.CopyObjectInput, _ ...func(*s3.Options),
) (*s3.CopyObjectOutput, error) {
	f.copied = in
	if f.err != nil {
		return nil, f.err
	}
	return &s3.CopyObjectOutput{}, nil
}

// fakePresign answers the signing a bucket asks for and records what it signed.
type fakePresign struct {
	getInput *s3.GetObjectInput
	putInput *s3.PutObjectInput
	err      error
}

func (f *fakePresign) PresignGetObject(
	_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.PresignOptions),
) (*v4.PresignedHTTPRequest, error) {
	f.getInput = in
	if f.err != nil {
		return nil, f.err
	}
	return &v4.PresignedHTTPRequest{URL: "https://orders-prod.s3/read"}, nil
}

func (f *fakePresign) PresignPutObject(
	_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.PresignOptions),
) (*v4.PresignedHTTPRequest, error) {
	f.putInput = in
	if f.err != nil {
		return nil, f.err
	}
	return &v4.PresignedHTTPRequest{URL: "https://orders-prod.s3/write"}, nil
}

func (f *fakeS3) PutObject(
	_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	body, _ := io.ReadAll(in.Body)
	f.body = string(body)
	f.put = in
	return &s3.PutObjectOutput{}, f.err
}

func (f *fakeS3) DeleteObject(
	_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	f.deleted = in
	return &s3.DeleteObjectOutput{}, f.err
}

func (f *fakeS3) ListObjectsV2(
	_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options),
) (*s3.ListObjectsV2Output, error) {
	f.listed = append(f.listed, in)
	if f.err != nil {
		return nil, f.err
	}
	return f.pages[min(len(f.listed)-1, len(f.pages)-1)], nil
}

func (s *BucketTestSuite) bucket(api awsbucket.API) bucket.Store {
	store, err := awsbucket.Buckets(api, nil)(awstest.SimpleRef(resources.KindBucket, "ordersBucket", "orders-prod"))
	s.Require().NoError(err)
	return store
}

func (s *BucketTestSuite) Test_a_failure_that_is_not_absence_stays_a_failure() {
	// Reporting a denied read as an empty bucket would have a handler carry on
	// with nothing, which is the worst of both.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "AccessDenied"}}

	_, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json")

	s.Require().Error(err)
	s.NotErrorIs(err, bucket.ErrNotFound)
	s.Contains(err.Error(), "AccessDenied")
}

func (s *BucketTestSuite) Test_an_object_is_written_with_what_was_asked_for() {
	api := &fakeS3{}

	_, err := s.bucket(api).Put(awstest.Ctx(), "orders/1.json", strings.NewReader(`{"id":1}`),
		func(o *bucket.PutOptions) { o.ContentType = "application/json" },
		func(o *bucket.PutOptions) { o.Metadata = map[string]string{"source": "orders"} },
	)

	s.Require().NoError(err)
	s.Equal(`{"id":1}`, api.body)
	s.Equal("orders-prod", aws.ToString(api.put.Bucket))
	s.Equal("application/json", aws.ToString(api.put.ContentType))
	s.Equal(map[string]string{"source": "orders"}, api.put.Metadata)
}

func (s *BucketTestSuite) Test_a_cursor_from_somewhere_else_is_refused() {
	// A cursor reaches a handler from a client and is the one input here an
	// outsider chooses, so it is checked rather than passed on.
	api := &fakeS3{}

	_, _, err := s.bucket(api).List(awstest.Ctx(), "orders/",
		func(o *bucket.ListOptions) { o.Cursor = "not a cursor!!" })

	s.Require().Error(err)
	s.Contains(err.Error(), "not one this store produced")
	s.Empty(api.listed, "nothing should have been asked of S3")
}

func (s *BucketTestSuite) Test_reading_a_listing_through_follows_every_page() {
	// What a handler that wants the lot uses, and what keeps the one-page
	// method from being the only shape on offer.
	api := &fakeS3{pages: []*s3.ListObjectsV2Output{
		{Contents: []s3types.Object{object("a"), object("b")}, IsTruncated: aws.Bool(true)},
		{Contents: []s3types.Object{object("c")}, IsTruncated: aws.Bool(false)},
	}}

	var keys []string
	for object, err := range bucket.Objects(awstest.Ctx(), s.bucket(api), "orders/") {
		s.Require().NoError(err)
		keys = append(keys, object.Key)
	}

	s.Equal([]string{"a", "b", "c"}, keys)
	s.Len(api.listed, 2)
}

func (s *BucketTestSuite) Test_stopping_early_stops_the_fetching() {
	// A loop that breaks on the first match should pay for one page rather
	// than for the whole prefix.
	api := &fakeS3{pages: []*s3.ListObjectsV2Output{
		{Contents: []s3types.Object{object("a"), object("b")}, IsTruncated: aws.Bool(true)},
		{Contents: []s3types.Object{object("c")}, IsTruncated: aws.Bool(false)},
	}}

	for object, err := range bucket.Objects(awstest.Ctx(), s.bucket(api), "orders/") {
		s.Require().NoError(err)
		if object.Key == "a" {
			break
		}
	}

	s.Len(api.listed, 1, "a second page should not have been asked for")
}

func (s *BucketTestSuite) Test_a_failure_ends_the_reading_rather_than_being_yielded_twice() {
	// A handler that ignores it would otherwise read a partial listing as a
	// complete one.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "AccessDenied"}}

	var yielded int
	var last error
	for _, err := range bucket.Objects(awstest.Ctx(), s.bucket(api), "orders/") {
		yielded++
		last = err
	}

	s.Equal(1, yielded)
	s.Require().Error(last)
	s.Contains(last.Error(), "AccessDenied")
}

func (s *BucketTestSuite) Test_a_listing_reports_what_it_found_about_each_object() {
	modified := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	api := &fakeS3{pages: []*s3.ListObjectsV2Output{{
		Contents: []s3types.Object{{
			Key:          aws.String("orders/1.json"),
			Size:         aws.Int64(42),
			LastModified: aws.Time(modified),
			ETag:         aws.String(`"abc"`),
		}},
	}}}

	found, _, err := s.bucket(api).List(awstest.Ctx(), "orders/")

	s.Require().NoError(err)
	s.Equal([]bucket.ObjectInfo{{
		Key:          "orders/1.json",
		Size:         42,
		LastModified: modified,
		ETag:         `"abc"`,
	}}, found)
}

func (s *BucketTestSuite) Test_a_bucket_the_deployment_never_recorded_says_so() {
	store, err := awsbucket.Buckets(&fakeS3{}, nil)(awstest.Ref(resources.KindBucket, "ordersBucket", nil))
	s.Require().NoError(err)

	_, err = store.Get(awstest.Ctx(), "orders/1.json")

	s.Require().Error(err)
	s.Contains(err.Error(), "ordersBucket")
}

func (s *BucketTestSuite) signing(api awsbucket.API, presign awsbucket.PresignAPI) bucket.Store {
	store, err := awsbucket.Buckets(api, presign)(
		awstest.SimpleRef(resources.KindBucket, "ordersBucket", "orders-prod"),
	)
	s.Require().NoError(err)
	return store
}

func (s *BucketTestSuite) Test_a_read_passes_on_what_s3_said_about_the_object() {
	// Both come back from the one request S3 already answered with, so a
	// handler serving a download does not pay for a second to learn the type.
	modified := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	api := &fakeS3{body: `{"id":1}`, got: &s3.GetObjectOutput{
		ContentLength: aws.Int64(8),
		ContentType:   aws.String("application/json"),
		LastModified:  aws.Time(modified),
		ETag:          aws.String(`"abc"`),
		Metadata:      map[string]string{"source": "orders"},
	}}

	object, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	defer object.Body.Close()
	body, err := io.ReadAll(object.Body)
	s.Require().NoError(err)
	s.Equal(`{"id":1}`, string(body))
	s.Equal(bucket.ObjectInfo{
		Key:          "orders/1.json",
		Size:         8,
		LastModified: modified,
		ETag:         `"abc"`,
		ContentType:  "application/json",
		Metadata:     map[string]string{"source": "orders"},
	}, object.Info)
	s.Equal(int64(8), object.ContentLength, "a whole read yields the whole object")
}

func (s *BucketTestSuite) Test_a_range_becomes_the_positions_http_states_one_with() {
	// HTTP ranges are a first and a last byte and include both, while a Range
	// is a start and a length, so the end is one short of their sum.
	cases := []struct {
		name  string
		asked bucket.Range
		want  string
	}{
		{"a length from a start", bucket.Range{Start: 10, Length: 10}, "bytes=10-19"},
		{"the first byte alone", bucket.Range{Length: 1}, "bytes=0-0"},
		{"a start to the end", bucket.Range{Start: 10}, "bytes=10-"},
	}

	for _, test := range cases {
		s.Run(test.name, func() {
			api := &fakeS3{}

			_, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
				func(o *bucket.GetOptions) {
					o.Range = test.asked
				})

			s.Require().NoError(err)
			s.Equal(test.want, aws.ToString(api.get.Range))
		})
	}
}

func (s *BucketTestSuite) Test_a_ranged_read_reports_the_whole_size_and_the_parts_length() {
	// A ranged answer carries the length of the part and the whole in the
	// content range. Reporting the part as Size would make Size mean two
	// things, so the part is its own field.
	api := &fakeS3{body: "0123456789", got: &s3.GetObjectOutput{
		ContentLength: aws.Int64(10),
		ContentRange:  aws.String("bytes 0-9/2048"),
	}}

	object, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
		func(o *bucket.GetOptions) {
			o.Range = bucket.Range{Length: 10}
		})

	s.Require().NoError(err)
	defer object.Body.Close()
	s.Equal(int64(2048), object.Info.Size)
	s.Equal(int64(10), object.ContentLength)
}

func (s *BucketTestSuite) Test_a_range_overrunning_the_object_reports_the_bytes_that_were_left() {
	// A store satisfies a range whose end is past the object with the bytes
	// that remain, so the length asked for is not the length that comes back
	// and a caller cannot work this out from the range alone.
	api := &fakeS3{body: "01234567", got: &s3.GetObjectOutput{
		ContentLength: aws.Int64(8),
		ContentRange:  aws.String("bytes 2040-2047/2048"),
	}}

	object, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
		func(o *bucket.GetOptions) {
			o.Range = bucket.Range{Start: 2040, Length: 1000}
		})

	s.Require().NoError(err)
	defer object.Body.Close()
	s.Equal(int64(2048), object.Info.Size)
	s.Equal(int64(8), object.ContentLength)
}

func (s *BucketTestSuite) Test_a_range_read_to_the_end_reports_what_came_back() {
	// A Range with no Length reads to the end, so the length is the store's to
	// report rather than anything the caller named.
	api := &fakeS3{body: "89", got: &s3.GetObjectOutput{
		ContentLength: aws.Int64(2),
		ContentRange:  aws.String("bytes 8-9/10"),
	}}

	object, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
		func(o *bucket.GetOptions) {
			o.Range = bucket.Range{Start: 8}
		})

	s.Require().NoError(err)
	defer object.Body.Close()
	s.Equal(int64(10), object.Info.Size)
	s.Equal(int64(2), object.ContentLength)
}

func (s *BucketTestSuite) Test_the_metadata_of_an_object_is_read_without_fetching_it() {
	modified := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	api := &fakeS3{headed: &s3.HeadObjectOutput{
		ContentLength: aws.Int64(42),
		ContentType:   aws.String("application/json"),
		LastModified:  aws.Time(modified),
		ETag:          aws.String(`"abc"`),
		Metadata:      map[string]string{"source": "orders"},
	}}

	info, err := s.bucket(api).Info(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	s.Equal(bucket.ObjectInfo{
		Key:          "orders/1.json",
		Size:         42,
		LastModified: modified,
		ETag:         `"abc"`,
		ContentType:  "application/json",
		Metadata:     map[string]string{"source": "orders"},
	}, info)
	s.Equal("orders-prod", aws.ToString(api.head.Bucket))
	s.Nil(api.get, "the object itself should not have been fetched")
}

func (s *BucketTestSuite) Test_the_metadata_of_an_object_that_is_not_there_is_an_absence() {
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "NotFound"}}

	_, err := s.bucket(api).Info(awstest.Ctx(), "orders/1.json")

	s.Require().Error(err)
	s.ErrorIs(err, bucket.ErrNotFound)
	s.Contains(err.Error(), "ordersBucket")
}

func (s *BucketTestSuite) Test_an_object_that_is_not_there_does_not_exist() {
	// The question was whether it is there, so an answer of no is an answer
	// rather than a failure.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "NotFound"}}

	exists, err := s.bucket(api).Exists(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	s.False(exists)
}

func (s *BucketTestSuite) Test_an_object_that_is_there_exists() {
	api := &fakeS3{}

	exists, err := s.bucket(api).Exists(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	s.True(exists)
}

func (s *BucketTestSuite) Test_a_failure_that_is_not_absence_is_not_an_answer_about_existence() {
	// Reporting a denied read as a missing object would have a handler write
	// over something it could not see.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "AccessDenied"}}

	_, err := s.bucket(api).Exists(awstest.Ctx(), "orders/1.json")

	s.Require().Error(err)
	s.NotErrorIs(err, bucket.ErrNotFound)
}

func (s *BucketTestSuite) Test_a_version_can_be_named_on_a_presence_check() {
	api := &fakeS3{}

	exists, err := s.bucket(api).Exists(awstest.Ctx(), "orders/1.json",
		func(o *bucket.ExistsOptions) {
			o.VersionID = "v1"
		})

	s.Require().NoError(err)
	s.True(exists)
	s.Equal("v1", aws.ToString(api.head.VersionId))
}

func (s *BucketTestSuite) Test_a_presence_check_naming_no_version_asks_about_the_current_one() {
	api := &fakeS3{}

	_, err := s.bucket(api).Exists(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	s.Nil(api.head.VersionId)
}

func (s *BucketTestSuite) Test_a_copy_within_a_bucket_names_the_source_as_a_path() {
	api := &fakeS3{}

	_, err := s.bucket(api).Copy(awstest.Ctx(), "orders/1.json",
		bucket.Destination{Key: "archive/1.json"})

	s.Require().NoError(err)
	s.Equal("orders-prod%2Forders%2F1.json", aws.ToString(api.copied.CopySource))
	s.Equal("orders-prod", aws.ToString(api.copied.Bucket))
	s.Equal("archive/1.json", aws.ToString(api.copied.Key))
	s.Nil(api.put, "the content should not have passed through the handler")
}

func (s *BucketTestSuite) Test_a_copy_carries_the_source_metadata_unless_told_otherwise() {
	api := &fakeS3{}

	_, err := s.bucket(api).Copy(awstest.Ctx(), "orders/1.json",
		bucket.Destination{Key: "archive/1.json"})

	s.Require().NoError(err)
	s.Empty(api.copied.MetadataDirective, "copying it over is what S3 does by default")
}

func (s *BucketTestSuite) Test_a_copy_given_either_of_them_replaces_both() {
	// S3 carries the two together, so there is no overriding one and
	// inheriting the other.
	api := &fakeS3{}

	_, err := s.bucket(api).Copy(awstest.Ctx(), "orders/1.json",
		bucket.Destination{Key: "archive/1.json"},
		func(o *bucket.CopyOptions) {
			o.ContentType = "application/json"
		},
	)

	s.Require().NoError(err)
	s.Equal(s3types.MetadataDirectiveReplace, api.copied.MetadataDirective)
	s.Equal("application/json", aws.ToString(api.copied.ContentType))
}

func (s *BucketTestSuite) Test_a_copy_into_another_bucket_resolves_that_buckets_handle() {
	// A handle rather than a name, because the name a deployment gave a bucket
	// is not something a handler is written against.
	api := &fakeS3{}
	archive, err := awsbucket.Buckets(api, nil)(
		awstest.SimpleRef(resources.KindBucket, "archiveBucket", "archive-prod"))
	s.Require().NoError(err)

	_, err = s.bucket(api).Copy(awstest.Ctx(), "orders/1.json",
		bucket.Destination{Key: "1.json", Store: archive})

	s.Require().NoError(err)
	s.Equal("archive-prod", aws.ToString(api.copied.Bucket), "the destination")
	s.Equal("orders-prod%2Forders%2F1.json", aws.ToString(api.copied.CopySource))
}

func (s *BucketTestSuite) Test_a_copy_into_a_bucket_outside_this_region_resolves_it_the_same_way() {
	// A bucket the blueprint declares as external records the region it is in,
	// since an S3 ARN carries none. The handle resolves the same way; what the
	// region decides is which client the request is sent with, and S3 routes a
	// copy by the bucket being written to rather than the one being read.
	api := &fakeS3{}
	archive, err := awsbucket.Buckets(api, nil)(awstest.Ref(
		resources.KindBucket, "archiveBucket", map[string]string{
			"archiveBucket":        "acme-central-archive",
			"archiveBucket_region": "us-east-1",
		}))
	s.Require().NoError(err)

	_, err = s.bucket(api).Copy(awstest.Ctx(), "orders/1.json",
		bucket.Destination{Key: "1.json", Store: archive})

	s.Require().NoError(err)
	s.Equal("acme-central-archive", aws.ToString(api.copied.Bucket))
	s.Equal("orders-prod%2Forders%2F1.json", aws.ToString(api.copied.CopySource))
}

func (s *BucketTestSuite) Test_a_bucket_reached_from_the_wrong_region_says_that_rather_than_the_code() {
	// S3 refuses a request that reaches the wrong region rather than routing
	// it, and neither answer reads as "the region is wrong": one is a redirect
	// and the other a complaint about the signature.
	cases := []struct{ name, code string }{
		{"a redirect to the bucket's own region", "PermanentRedirect"},
		{"a signature for the wrong region", "AuthorizationHeaderMalformed"},
	}

	for _, test := range cases {
		s.Run(test.name, func() {
			api := &fakeS3{err: &smithy.GenericAPIError{Code: test.code}}

			_, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json")

			s.Require().Error(err)
			s.Contains(err.Error(), "not in the region it is being reached from")
			s.Contains(err.Error(), "record the region it is in")
			s.NotErrorIs(err, bucket.ErrNotFound,
				"the bucket is unreachable rather than empty")
		})
	}
}

func (s *BucketTestSuite) Test_a_copy_into_something_that_is_not_an_s3_bucket_says_so() {
	api := &fakeS3{}

	_, err := s.bucket(api).Copy(awstest.Ctx(), "orders/1.json",
		bucket.Destination{Key: "1.json", Store: notABucket{}})

	s.Require().Error(err)
	s.Contains(err.Error(), "not a bucket on AWS")
	s.Nil(api.copied, "nothing should have been asked of S3")
}

func (s *BucketTestSuite) Test_a_url_is_signed_for_reading_an_object() {
	api, presign := &fakeS3{}, &fakePresign{}

	signed, err := s.signing(api, presign).SignedURL(
		awstest.Ctx(), "orders/1.json", bucket.SignRead, time.Hour)

	s.Require().NoError(err)
	s.Equal("https://orders-prod.s3/read", signed.URL)
	s.WithinDuration(time.Now().Add(time.Hour), signed.ExpiresAt, time.Minute)
	s.Require().NotNil(presign.getInput)
	s.Equal("orders-prod", aws.ToString(presign.getInput.Bucket))
	s.Equal("orders/1.json", aws.ToString(presign.getInput.Key))
	s.Nil(presign.putInput, "a read should not have been signed as a write")
}

func (s *BucketTestSuite) Test_a_url_is_signed_for_writing_an_object() {
	// What lets a browser upload without the bytes passing through a handler.
	api, presign := &fakeS3{}, &fakePresign{}

	signed, err := s.signing(api, presign).SignedURL(
		awstest.Ctx(), "uploads/1.png", bucket.SignWrite, 15*time.Minute)

	s.Require().NoError(err)
	s.Equal("https://orders-prod.s3/write", signed.URL)
	s.Require().NotNil(presign.putInput)
	s.Equal("uploads/1.png", aws.ToString(presign.putInput.Key))
	s.Nil(presign.getInput)
}

func (s *BucketTestSuite) Test_a_content_type_is_signed_into_a_write_url() {
	// Signed in, so an upload declaring something else is refused rather than
	// stored as a type nobody expected.
	api, presign := &fakeS3{}, &fakePresign{}

	_, err := s.signing(api, presign).SignedURL(
		awstest.Ctx(), "uploads/1.png", bucket.SignWrite, 15*time.Minute,
		func(o *bucket.SignOptions) {
			o.ContentType = "image/png"
		})

	s.Require().NoError(err)
	s.Equal("image/png", aws.ToString(presign.putInput.ContentType))
}

func (s *BucketTestSuite) Test_a_content_type_is_ignored_by_a_read_url() {
	api, presign := &fakeS3{}, &fakePresign{}

	_, err := s.signing(api, presign).SignedURL(
		awstest.Ctx(), "orders/1.json", bucket.SignRead, time.Hour,
		func(o *bucket.SignOptions) {
			o.ContentType = "application/json"
		})

	s.Require().NoError(err)
	s.Nil(presign.getInput.ResponseContentType, "a read has nothing to constrain")
}

func (s *BucketTestSuite) Test_an_action_a_url_cannot_be_signed_for_says_what_can() {
	api, presign := &fakeS3{}, &fakePresign{}

	_, err := s.signing(api, presign).SignedURL(
		awstest.Ctx(), "orders/1.json", bucket.SignAction("delete"), time.Hour)

	s.Require().Error(err)
	s.Contains(err.Error(), `"delete"`)
	s.Contains(err.Error(), `"read"`)
	s.Contains(err.Error(), `"write"`)
}

func (s *BucketTestSuite) Test_a_version_that_is_not_there_is_an_absence() {
	// S3 says NoSuchVersion rather than NoSuchKey for a version that was
	// removed, which is a different answer from S3 and the same one to a
	// handler.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "NoSuchVersion"}}

	_, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
		func(o *bucket.GetOptions) { o.VersionID = "gone" })

	s.Require().Error(err)
	s.ErrorIs(err, bucket.ErrNotFound)
}

func (s *BucketTestSuite) Test_a_version_that_records_a_deletion_holds_no_object() {
	// A deletion marker is a version with nothing to read, and S3 answers a
	// read of one with 405 rather than 404, providing a different answer to the same
	// question.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "MethodNotAllowed"}}

	_, getErr := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
		func(o *bucket.GetOptions) {
			o.VersionID = "a-marker"
		})
	_, infoErr := s.bucket(api).Info(awstest.Ctx(), "orders/1.json",
		func(o *bucket.InfoOptions) {
			o.VersionID = "a-marker"
		})
	exists, existsErr := s.bucket(api).Exists(awstest.Ctx(), "orders/1.json",
		func(o *bucket.ExistsOptions) {
			o.VersionID = "a-marker"
		})

	s.ErrorIs(getErr, bucket.ErrNotFound)
	s.ErrorIs(infoErr, bucket.ErrNotFound)
	s.Require().NoError(existsErr, "a marker is an answer of no rather than a failure")
	s.False(exists)
}

func (s *BucketTestSuite) Test_a_refusal_that_is_not_a_read_stays_a_failure() {
	// 405 from a read is a marker; from anything else it is not, so it must not
	// be read as absence.
	api := &fakeS3{err: &smithy.GenericAPIError{Code: "MethodNotAllowed"}}

	err := s.bucket(api).Delete(awstest.Ctx(), "orders/1.json")

	s.Require().Error(err)
	s.NotErrorIs(err, bucket.ErrNotFound)
}

func (s *BucketTestSuite) Test_a_version_can_be_named_on_a_read() {
	api := &fakeS3{body: "the old one"}

	_, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json",
		func(o *bucket.GetOptions) { o.VersionID = "v1" })

	s.Require().NoError(err)
	s.Equal("v1", aws.ToString(api.get.VersionId))
}

func (s *BucketTestSuite) Test_a_read_reports_the_version_it_read() {
	// What a handler stores if the object may need reading back as it was
	// written, since it is the only way to reach a version without listing.
	api := &fakeS3{got: &s3.GetObjectOutput{VersionId: aws.String("v2")}}

	object, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	defer object.Body.Close()
	s.Equal("v2", object.Info.VersionID)
}

func (s *BucketTestSuite) Test_a_read_of_a_bucket_without_versioning_names_no_version() {
	api := &fakeS3{}

	_, err := s.bucket(api).Get(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	s.Nil(api.get.VersionId, "a request carrying no version is the current one")
}

func (s *BucketTestSuite) Test_a_version_can_be_named_on_a_metadata_read() {
	api := &fakeS3{headed: &s3.HeadObjectOutput{VersionId: aws.String("v1")}}

	info, err := s.bucket(api).Info(awstest.Ctx(), "orders/1.json",
		func(o *bucket.InfoOptions) {
			o.VersionID = "v1"
		})

	s.Require().NoError(err)
	s.Equal("v1", aws.ToString(api.head.VersionId))
	s.Equal("v1", info.VersionID)
}

func (s *BucketTestSuite) Test_a_version_can_be_named_on_a_delete() {
	// Without one a delete writes a marker and the versions stay; with one the
	// version is removed for good.
	api := &fakeS3{}

	err := s.bucket(api).Delete(awstest.Ctx(), "orders/1.json",
		func(o *bucket.DeleteOptions) {
			o.VersionID = "v1"
		})

	s.Require().NoError(err)
	s.Equal("v1", aws.ToString(api.deleted.VersionId))
}

func (s *BucketTestSuite) Test_a_delete_naming_no_version_hides_the_current_one() {
	api := &fakeS3{}

	err := s.bucket(api).Delete(awstest.Ctx(), "orders/1.json")

	s.Require().NoError(err)
	s.Nil(api.deleted.VersionId)
}

func (s *BucketTestSuite) Test_the_versions_of_a_key_are_listed_newest_first() {
	// S3 answers with the versions and the markers in two lists already ordered
	// by key and then by age, and a handler wants one list.
	newest := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	middle := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	api := &fakeS3{versionPages: []*s3.ListObjectVersionsOutput{{
		Versions: []s3types.ObjectVersion{
			{
				Key: aws.String("orders/1.json"), VersionId: aws.String("v2"),
				Size: aws.Int64(20), LastModified: aws.Time(middle), ETag: aws.String(`"b"`),
			},
			{
				Key: aws.String("orders/1.json"), VersionId: aws.String("v1"),
				Size: aws.Int64(10), LastModified: aws.Time(oldest), ETag: aws.String(`"a"`),
			},
		},
		DeleteMarkers: []s3types.DeleteMarkerEntry{{
			Key: aws.String("orders/1.json"), VersionId: aws.String("v3"),
			LastModified: aws.Time(newest), IsLatest: aws.Bool(true),
		}},
	}}}

	found, _, err := s.bucket(api).Versions(awstest.Ctx(), "orders/")

	s.Require().NoError(err)
	s.Equal([]bucket.ObjectVersion{
		{
			Key: "orders/1.json", VersionID: "v3", LastModified: newest,
			Current: true, DeleteMarker: true,
		},
		{Key: "orders/1.json", VersionID: "v2", Size: 20, LastModified: middle, ETag: `"b"`},
		{Key: "orders/1.json", VersionID: "v1", Size: 10, LastModified: oldest, ETag: `"a"`},
	}, found)
}

func (s *BucketTestSuite) Test_the_versions_of_several_keys_stay_grouped_by_key() {
	modified := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	api := &fakeS3{versionPages: []*s3.ListObjectVersionsOutput{{
		Versions: []s3types.ObjectVersion{
			{Key: aws.String("a"), VersionId: aws.String("a1"), LastModified: aws.Time(modified)},
			{Key: aws.String("b"), VersionId: aws.String("b1"), LastModified: aws.Time(modified)},
		},
		DeleteMarkers: []s3types.DeleteMarkerEntry{{
			Key: aws.String("a"), VersionId: aws.String("a2"), LastModified: aws.Time(modified),
		}},
	}}}

	found, _, err := s.bucket(api).Versions(awstest.Ctx(), "")

	s.Require().NoError(err)
	s.Equal([]string{"a", "a", "b"}, []string{found[0].Key, found[1].Key, found[2].Key})
}

func (s *BucketTestSuite) Test_reading_the_versions_through_follows_every_page() {
	modified := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	page := func(key, version string, truncated bool) *s3.ListObjectVersionsOutput {
		return &s3.ListObjectVersionsOutput{
			Versions: []s3types.ObjectVersion{{
				Key: aws.String(key), VersionId: aws.String(version),
				LastModified: aws.Time(modified),
			}},
			IsTruncated: aws.Bool(truncated),
		}
	}
	api := &fakeS3{versionPages: []*s3.ListObjectVersionsOutput{
		page("a", "a1", true),
		page("b", "b1", false),
	}}

	var ids []string
	for version, err := range bucket.AllVersions(awstest.Ctx(), s.bucket(api), "") {
		s.Require().NoError(err)
		ids = append(ids, version.VersionID)
	}

	s.Equal([]string{"a1", "b1"}, ids)
	s.Len(api.versions, 2)
}

func (s *BucketTestSuite) Test_several_objects_are_deleted_in_one_request() {
	api := &fakeS3{}
	refs := []bucket.ObjectRef{
		{Key: "orders/1.json"},
		{Key: "orders/2.json", VersionID: "v1"},
	}

	result, err := s.bucket(api).DeleteMany(awstest.Ctx(), refs)

	s.Require().NoError(err)
	s.Require().Len(api.deletedMany, 1, "one request rather than one per object")
	sent := api.deletedMany[0].Delete.Objects
	s.Equal("orders/1.json", aws.ToString(sent[0].Key))
	s.Nil(sent[0].VersionId)
	s.Equal("v1", aws.ToString(sent[1].VersionId))
	s.ElementsMatch(refs, result.Deleted)
	s.Empty(result.Failed)
	s.Empty(result.Unsent)
}

func (s *BucketTestSuite) Test_a_delete_of_more_than_s3_takes_at_once_is_chunked() {
	api := &fakeS3{}
	refs := make([]bucket.ObjectRef, 2500)
	for i := range refs {
		refs[i] = bucket.ObjectRef{Key: fmt.Sprintf("orders/%d.json", i)}
	}

	result, err := s.bucket(api).DeleteMany(awstest.Ctx(), refs)

	s.Require().NoError(err)
	s.Require().Len(api.deletedMany, 3, "1000 at a time is S3's own ceiling")
	s.Len(api.deletedMany[0].Delete.Objects, 1000)
	s.Len(api.deletedMany[2].Delete.Objects, 500)
	s.Len(result.Deleted, 2500)
}

func (s *BucketTestSuite) Test_a_refused_delete_is_reported_rather_than_raised() {
	// S3 takes a request partially, and a caller handed an error for the whole
	// thing cannot tell which of their objects are gone.
	api := &fakeS3{deleteAnswer: func(in *s3.DeleteObjectsInput) *s3.DeleteObjectsOutput {
		return &s3.DeleteObjectsOutput{
			Deleted: []s3types.DeletedObject{{Key: in.Delete.Objects[0].Key}},
			Errors: []s3types.Error{{
				Key:     in.Delete.Objects[1].Key,
				Code:    aws.String("AccessDenied"),
				Message: aws.String("not yours to delete"),
			}},
		}
	}}

	result, err := s.bucket(api).DeleteMany(awstest.Ctx(), []bucket.ObjectRef{
		{Key: "orders/1.json"},
		{Key: "orders/2.json"},
	})

	s.Require().NoError(err)
	s.Equal([]bucket.ObjectRef{{Key: "orders/1.json"}}, result.Deleted)
	s.Equal([]bucket.DeleteFailure{{
		Ref:     bucket.ObjectRef{Key: "orders/2.json"},
		Code:    "AccessDenied",
		Message: "not yours to delete",
	}}, result.Failed)
}

func (s *BucketTestSuite) Test_a_failed_delete_request_accounts_for_every_object() {
	api := &fakeS3{failDeleteFrom: 2}
	refs := make([]bucket.ObjectRef, 1500)
	for i := range refs {
		refs[i] = bucket.ObjectRef{Key: fmt.Sprintf("orders/%d.json", i)}
	}

	result, err := s.bucket(api).DeleteMany(awstest.Ctx(), refs)

	s.Require().Error(err)
	s.Len(result.Deleted, 1000, "the first request's objects are gone and are reported")
	s.Require().Len(result.Unsent, 500)
	s.Equal("orders/1000.json", result.Unsent[0].Ref.Key)
	s.ErrorIs(result.Unsent[0].Err, err)
}

func (s *BucketTestSuite) Test_deleting_nothing_does_not_call_s3() {
	api := &fakeS3{}

	result, err := s.bucket(api).DeleteMany(awstest.Ctx(), nil)

	s.Require().NoError(err)
	s.Empty(result.Deleted)
	s.Empty(api.deletedMany)
}

// notABucket is a store from no provider, which is what a copy cannot resolve.
type notABucket struct {
	bucket.Store
}

func object(key string) s3types.Object {
	return s3types.Object{Key: aws.String(key), Size: aws.Int64(1)}
}
