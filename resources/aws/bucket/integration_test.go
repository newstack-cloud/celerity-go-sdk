//go:build integration

// The S3 client against the service it calls.
//
// A stand-in (mock in unit tests) cannot establish that a request is shaped the way S3 expects.
// Paging a listing, presigning a URL and writing an object through the transfer
// manager are all things only the service can answer for.
//
// Run with: bash scripts/run-tests.sh --with-integration
package bucket_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsresources "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

const bucketBaseName = "orders-integration"

type BucketIntegrationTestSuite struct {
	suite.Suite

	target        awstest.Target
	provider      *awsresources.Provider
	s3            *s3.Client
	bucketName    string
	versionedName string
	// run distinguishes this run's keys from an earlier one's. The buckets are
	// reused between runs rather than torn down, and a versioned bucket keeps
	// every version ever written to a key, so a test that counts them has to
	// write to a key isolated from other runs.
	run string
}

func TestBucketIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(BucketIntegrationTestSuite))
}

// SetupSuite points the SDK at the target and creates the bucket a deployment
// would have created, and nothing else.
func (s *BucketIntegrationTestSuite) SetupSuite() {
	var cfg aws.Config
	s.target, cfg = awstest.AWS(s.T())
	s.bucketName = s.target.Name(bucketBaseName)
	// Path style only against an emulator, because a bucket addressed as a
	// subdomain needs DNS that an endpoint on localhost does not have. Against
	// an account the SDK's own default is the one a deployment gets.
	emulated := s.target.Emulator
	s.s3 = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = emulated
	})

	s.target.Reachable(s.T(), func(ctx context.Context) error {
		_, err := s.s3.ListBuckets(ctx, &s3.ListBucketsInput{})
		return err
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := s.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.bucketName),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraintEuWest2,
		},
	})
	if err != nil {
		var owned *s3types.BucketAlreadyOwnedByYou
		s.Require().ErrorAs(err, &owned, "creating the bucket this suite reads")
	}

	s.versionedName = s.target.Name(bucketBaseName + "-versioned")
	_, err = s.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.versionedName),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraintEuWest2,
		},
	})
	if err != nil {
		var owned *s3types.BucketAlreadyOwnedByYou
		s.Require().ErrorAs(err, &owned, "creating the versioned bucket this suite reads")
	}
	_, err = s.s3.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(s.versionedName),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	s.Require().NoError(err, "turning versioning on, which is what a blueprint does")

	s.run = strconv.FormatInt(time.Now().UnixNano(), 36)
	s.provider = awsresources.New()
}

func (s *BucketIntegrationTestSuite) Test_an_object_is_written_read_listed_and_deleted() {
	ctx := context.Background()
	store := s.bucket()

	s.put(ctx, "orders/1.json",
		strings.NewReader(`{"id":1}`),
		func(o *bucket.PutOptions) { o.ContentType = "application/json" })

	object, err := store.Get(ctx, "orders/1.json")
	s.Require().NoError(err)
	defer object.Body.Close()
	read, err := io.ReadAll(object.Body)
	s.Require().NoError(err)
	s.Equal(`{"id":1}`, string(read))
	s.Equal("application/json", object.Info.ContentType,
		"the type it was written with should come back with the content")
	s.EqualValues(8, object.Info.Size)

	found, _, err := store.List(ctx, "orders/")
	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.Equal("orders/1.json", found[0].Key)
	s.EqualValues(8, found[0].Size)

	s.Require().NoError(store.Delete(ctx, "orders/1.json"))

	_, err = store.Get(ctx, "orders/1.json")
	s.Require().Error(err)
	s.ErrorIs(err, bucket.ErrNotFound,
		"S3's own way of saying absent should reach a handler as this")
}

func (s *BucketIntegrationTestSuite) Test_a_listing_resumes_where_the_last_page_stopped() {
	// Only a service that actually truncates can say whether a cursor resumes
	// where it should. A unit test mock agrees with whatever it is handed.
	ctx := context.Background()
	store := s.bucket()

	for i := range 3 {
		key := "paged/" + string(rune('a'+i))
		s.put(ctx, key, strings.NewReader("x"))
	}

	first, cursor, err := store.List(
		ctx, "paged/",
		func(o *bucket.ListOptions) {
			o.Limit = 2
		},
	)
	s.Require().NoError(err)
	s.Len(first, 2)
	s.Require().True(cursor.More())

	second, _, err := store.List(
		ctx, "paged/",
		func(o *bucket.ListOptions) {
			o.Cursor = string(cursor)
		},
	)
	s.Require().NoError(err)
	s.Require().Len(second, 1)
	s.Equal("paged/c", second[0].Key, "the page should start after the last one read")

	// And read through, which is what a handler wanting the lot does.
	var keys []string
	for object, err := range bucket.Objects(
		ctx, store, "paged/",
		func(o *bucket.ListOptions) {
			o.Limit = 2
		},
	) {
		s.Require().NoError(err)
		keys = append(keys, object.Key)
	}
	s.Equal([]string{"paged/a", "paged/b", "paged/c"}, keys)
}

func (s *BucketIntegrationTestSuite) Test_a_signed_url_can_be_fetched_without_credentials() {
	// What a signed URL is for. Nothing but the real signer and the real
	// service can establish that the signature is accepted.
	ctx := context.Background()
	store := s.bucket()
	s.put(ctx, "signed/1.txt", strings.NewReader("the object"))

	signed, err := store.SignedURL(ctx, "signed/1.txt", bucket.SignRead, 5*time.Minute)
	s.Require().NoError(err)
	s.WithinDuration(time.Now().Add(5*time.Minute), signed.ExpiresAt, time.Minute)

	res, err := http.Get(signed.URL)
	s.Require().NoError(err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	s.Require().NoError(err)
	s.Equal(http.StatusOK, res.StatusCode)
	s.Equal("the object", string(body))
}

func (s *BucketIntegrationTestSuite) Test_a_signed_url_can_be_uploaded_to_without_credentials() {
	// What lets a browser upload without the bytes passing through a handler,
	// and what only the real signer and the real service can establish.
	ctx := context.Background()
	store := s.bucket()

	signed, err := store.SignedURL(ctx, "signed/upload.txt", bucket.SignWrite, 5*time.Minute,
		func(o *bucket.SignOptions) {
			o.ContentType = "text/plain"
		})
	s.Require().NoError(err)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPut, signed.URL, strings.NewReader("uploaded without credentials"))
	s.Require().NoError(err)
	req.Header.Set("Content-Type", "text/plain")

	res, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	s.Require().NoError(res.Body.Close())
	s.Equal(http.StatusOK, res.StatusCode)

	stored, err := store.Get(ctx, "signed/upload.txt")
	s.Require().NoError(err)
	defer stored.Body.Close()
	read, err := io.ReadAll(stored.Body)
	s.Require().NoError(err)
	s.Equal("uploaded without credentials", string(read))
	s.Equal("text/plain", stored.Info.ContentType,
		"the type signed into the URL should be the type it was stored with")
}

func (s *BucketIntegrationTestSuite) Test_part_of_an_object_is_read_without_the_whole() {
	ctx := context.Background()
	store := s.bucket()
	s.put(ctx, "ranged/1.txt", strings.NewReader("0123456789"))

	object, err := store.Get(ctx, "ranged/1.txt",
		func(o *bucket.GetOptions) {
			o.Range = bucket.Range{Start: 2, Length: 3}
		})
	s.Require().NoError(err)
	defer object.Body.Close()

	read, err := io.ReadAll(object.Body)
	s.Require().NoError(err)
	s.Equal("234", string(read))
	s.EqualValues(10, object.Info.Size, "the whole object's size, not the part read")
	s.EqualValues(3, object.ContentLength, "the part read, not the whole object's size")
}

func (s *BucketIntegrationTestSuite) Test_a_range_the_object_cannot_fill_reads_what_is_left() {
	// The two cases a caller cannot work out from the range it asked for, firstly a
	// length that overruns the object, which the store satisfies with the bytes
	// that remain, and no length at all, which reads to the end.
	ctx := context.Background()
	store := s.bucket()
	s.put(ctx, "ranged/2.txt", strings.NewReader("0123456789"))

	overrun, err := store.Get(ctx, "ranged/2.txt",
		func(o *bucket.GetOptions) { o.Range = bucket.Range{Start: 8, Length: 100} })
	s.Require().NoError(err)
	defer overrun.Body.Close()
	read, err := io.ReadAll(overrun.Body)
	s.Require().NoError(err)
	s.Equal("89", string(read), "a range past the end is satisfied with what is left")
	s.EqualValues(10, overrun.Info.Size)
	s.EqualValues(2, overrun.ContentLength)

	toEnd, err := store.Get(ctx, "ranged/2.txt",
		func(o *bucket.GetOptions) {
			o.Range = bucket.Range{Start: 7}
		})
	s.Require().NoError(err)
	defer toEnd.Body.Close()
	rest, err := io.ReadAll(toEnd.Body)
	s.Require().NoError(err)
	s.Equal("789", string(rest))
	s.EqualValues(10, toEnd.Info.Size)
	s.EqualValues(3, toEnd.ContentLength)
}

func (s *BucketIntegrationTestSuite) Test_the_metadata_of_an_object_is_read_and_its_presence_told() {
	ctx := context.Background()
	store := s.bucket()
	s.put(ctx, "headed/1.json", strings.NewReader(`{"id":1}`),
		func(o *bucket.PutOptions) {
			o.ContentType = "application/json"
		},
		func(o *bucket.PutOptions) {
			o.Metadata = map[string]string{"source": "orders"}
		})

	info, err := store.Info(ctx, "headed/1.json")
	s.Require().NoError(err)
	s.EqualValues(8, info.Size)
	s.Equal("application/json", info.ContentType)
	s.Equal("orders", info.Metadata["source"],
		"what was stored alongside the object should come back")

	exists, err := store.Exists(ctx, "headed/1.json")
	s.Require().NoError(err)
	s.True(exists)

	exists, err = store.Exists(ctx, "headed/missing.json")
	s.Require().NoError(err)
	s.False(exists, "a key that holds nothing is an answer rather than a failure")

	_, err = store.Info(ctx, "headed/missing.json")
	s.ErrorIs(err, bucket.ErrNotFound)
}

func (s *BucketIntegrationTestSuite) Test_an_object_is_copied_by_the_service() {
	// The content does not pass through the handler, which only the service
	// can be held to.
	ctx := context.Background()
	store := s.bucket()
	s.put(ctx, "copied/source.json", strings.NewReader(`{"id":1}`),
		func(o *bucket.PutOptions) {
			o.ContentType = "application/json"
		},
		func(o *bucket.PutOptions) {
			o.Metadata = map[string]string{"source": "orders"}
		})

	s.copy(ctx, "copied/source.json",
		bucket.Destination{Key: "copied/carried.json"})

	carried, err := store.Info(ctx, "copied/carried.json")
	s.Require().NoError(err)
	s.Equal("application/json", carried.ContentType)
	s.Equal("orders", carried.Metadata["source"], "carried over from the source")

	s.copy(ctx, "copied/source.json",
		bucket.Destination{Key: "copied/replaced.json"},
		func(o *bucket.CopyOptions) {
			o.ContentType = "text/plain"
		},
		func(o *bucket.CopyOptions) {
			o.Metadata = map[string]string{"source": "archive"}
		})

	replaced, err := store.Info(ctx, "copied/replaced.json")
	s.Require().NoError(err)
	s.Equal("text/plain", replaced.ContentType)
	s.Equal("archive", replaced.Metadata["source"])

	object, err := store.Get(ctx, "copied/carried.json")
	s.Require().NoError(err)
	defer object.Body.Close()
	read, err := io.ReadAll(object.Body)
	s.Require().NoError(err)
	s.Equal(`{"id":1}`, string(read), "the content should have been copied too")
}

func (s *BucketIntegrationTestSuite) Test_a_key_with_characters_a_url_reserves_is_copied() {
	// The source reaches S3 as a path, so a key holding a question mark or a
	// hash would be read as the start of a query or a fragment.
	ctx := context.Background()
	store := s.bucket()
	key := "copied/what? and #1.json"
	s.put(ctx, key, strings.NewReader(`{"id":1}`))

	s.copy(ctx, key, bucket.Destination{Key: "copied/escaped.json"})

	object, err := store.Get(ctx, "copied/escaped.json")
	s.Require().NoError(err)
	defer object.Body.Close()
	read, err := io.ReadAll(object.Body)
	s.Require().NoError(err)
	s.Equal(`{"id":1}`, string(read))
}

// put and copy are the write half of this suite's setup, which every test does
// and none of which is what it is testing.
func (s *BucketIntegrationTestSuite) put(
	ctx context.Context, key string, body io.Reader, opts ...bucket.PutOption,
) bucket.PutResult {
	result, err := s.bucket().Put(ctx, key, body, opts...)
	s.Require().NoError(err)
	return result
}

func (s *BucketIntegrationTestSuite) copy(
	ctx context.Context, sourceKey string, dest bucket.Destination, opts ...bucket.CopyOption,
) bucket.CopyResult {
	result, err := s.bucket().Copy(ctx, sourceKey, dest, opts...)
	s.Require().NoError(err)
	return result
}

// versioned is the bucket a blueprint turned versioning on for, which is where
// every version a write leaves behind is kept.
func (s *BucketIntegrationTestSuite) versioned() bucket.Store {
	store, err := s.provider.Bucket(
		awstest.SimpleRef(resources.KindBucket, "archiveBucket", s.versionedName))
	s.Require().NoError(err)
	return store
}

func (s *BucketIntegrationTestSuite) Test_a_version_is_read_back_as_it_was_written() {
	// Only a bucket that actually keeps versions can say whether naming one
	// reads it, a unit test mock hands back whatever it was given.
	ctx := context.Background()
	store := s.versioned()

	first, err := store.Put(ctx, "versioned/1.txt", strings.NewReader("the first"))
	s.Require().NoError(err)
	s.Require().NotEmpty(first.VersionID, "a versioned bucket should name the version")

	second, err := store.Put(ctx, "versioned/1.txt", strings.NewReader("the second"))
	s.Require().NoError(err)
	s.NotEqual(first.VersionID, second.VersionID)

	current, err := store.Get(ctx, "versioned/1.txt")
	s.Require().NoError(err)
	defer current.Body.Close()
	read, err := io.ReadAll(current.Body)
	s.Require().NoError(err)
	s.Equal("the second", string(read), "a read naming no version is the current one")

	old, err := store.Get(ctx, "versioned/1.txt",
		func(o *bucket.GetOptions) {
			o.VersionID = first.VersionID
		})
	s.Require().NoError(err)
	defer old.Body.Close()
	read, err = io.ReadAll(old.Body)
	s.Require().NoError(err)
	s.Equal("the first", string(read))
	s.Equal(first.VersionID, old.Info.VersionID)

	info, err := store.Info(ctx, "versioned/1.txt",
		func(o *bucket.InfoOptions) {
			o.VersionID = first.VersionID
		})
	s.Require().NoError(err)
	s.EqualValues(9, info.Size, "the first version's size, not the current one's")
}

func (s *BucketIntegrationTestSuite) Test_a_delete_hides_the_versions_and_naming_one_removes_it() {
	// The behaviour versioning turns a delete into, which nothing but a real
	// versioned bucket does: a delete writes a marker and the content stays.
	ctx := context.Background()
	store := s.versioned()

	written, err := store.Put(ctx, "versioned/2.txt", strings.NewReader("still here"))
	s.Require().NoError(err)

	s.Require().NoError(store.Delete(ctx, "versioned/2.txt"))

	_, err = store.Get(ctx, "versioned/2.txt")
	s.ErrorIs(err, bucket.ErrNotFound, "the marker should hide it")

	hidden, err := store.Get(ctx, "versioned/2.txt",
		func(o *bucket.GetOptions) {
			o.VersionID = written.VersionID
		})
	s.Require().NoError(err, "the version itself should still be there")
	defer hidden.Body.Close()

	s.Require().NoError(store.Delete(ctx, "versioned/2.txt",
		func(o *bucket.DeleteOptions) {
			o.VersionID = written.VersionID
		}))

	_, err = store.Get(ctx, "versioned/2.txt",
		func(o *bucket.GetOptions) {
			o.VersionID = written.VersionID
		})
	s.ErrorIs(err, bucket.ErrNotFound, "naming the version should remove it for good")
}

func (s *BucketIntegrationTestSuite) Test_a_version_exists_while_the_current_one_does_not() {
	// The distinction naming a version makes, and one only a real versioned
	// bucket shows: a delete leaves a marker as the current version, so the key
	// has nothing current while every version under it is still there.
	ctx := context.Background()
	store := s.versioned()
	key := "versioned/presence-" + s.run + ".txt"

	written, err := store.Put(ctx, key, strings.NewReader("here"))
	s.Require().NoError(err)

	exists, err := store.Exists(ctx, key)
	s.Require().NoError(err)
	s.True(exists)

	s.Require().NoError(store.Delete(ctx, key))

	exists, err = store.Exists(ctx, key)
	s.Require().NoError(err)
	s.False(exists, "the marker is the current version, so nothing current is there")

	exists, err = store.Exists(ctx, key,
		func(o *bucket.ExistsOptions) {
			o.VersionID = written.VersionID
		})
	s.Require().NoError(err)
	s.True(exists, "the version itself is still there")

	s.Require().NoError(store.Delete(ctx, key,
		func(o *bucket.DeleteOptions) {
			o.VersionID = written.VersionID
		}))

	exists, err = store.Exists(ctx, key,
		func(o *bucket.ExistsOptions) {
			o.VersionID = written.VersionID
		})
	s.Require().NoError(err)
	s.False(exists, "and naming it after it was removed is an answer of no")
}

func (s *BucketIntegrationTestSuite) Test_a_marker_version_reads_as_an_absence() {
	// Versions hands back the markers along with the versions, so a marker's id
	// is the kind a caller passes straight back in. S3 answers a read of one
	// with 405 rather than 404, which only the real service shows.
	ctx := context.Background()
	store := s.versioned()
	key := "versioned/marker-" + s.run + ".txt"

	_, err := store.Put(ctx, key, strings.NewReader("here"))
	s.Require().NoError(err)
	s.Require().NoError(store.Delete(ctx, key))

	var markerID string
	for version, err := range bucket.AllVersions(ctx, store, key) {
		s.Require().NoError(err)
		if version.DeleteMarker {
			markerID = version.VersionID
		}
	}
	s.Require().NotEmpty(markerID, "the delete should have left one")

	_, err = store.Get(ctx, key, func(o *bucket.GetOptions) {
		o.VersionID = markerID
	})
	s.ErrorIs(err, bucket.ErrNotFound)

	_, err = store.Info(ctx, key, func(o *bucket.InfoOptions) {
		o.VersionID = markerID
	})
	s.ErrorIs(err, bucket.ErrNotFound)

	exists, err := store.Exists(ctx, key, func(o *bucket.ExistsOptions) {
		o.VersionID = markerID
	})
	s.Require().NoError(err, "a marker is an answer of no rather than a failure")
	s.False(exists)
}

func (s *BucketIntegrationTestSuite) Test_the_versions_of_a_key_are_listed_with_the_markers() {
	ctx := context.Background()
	store := s.versioned()
	key := "versioned/listed-" + s.run + ".txt"

	first, err := store.Put(ctx, key, strings.NewReader("one"))
	s.Require().NoError(err)
	second, err := store.Put(ctx, key, strings.NewReader("two"))
	s.Require().NoError(err)
	s.Require().NoError(store.Delete(ctx, key))

	var found []bucket.ObjectVersion
	for version, err := range bucket.AllVersions(ctx, store, key) {
		s.Require().NoError(err)
		found = append(found, version)
	}

	s.Require().Len(found, 3, "two writes and the marker the delete left")
	s.True(found[0].DeleteMarker, "newest first, and the newest is the marker")
	s.True(found[0].Current)
	s.Equal(second.VersionID, found[1].VersionID)
	s.Equal(first.VersionID, found[2].VersionID)
	s.False(found[1].DeleteMarker)
}

func (s *BucketIntegrationTestSuite) Test_several_objects_are_deleted_in_one_request() {
	ctx := context.Background()
	store := s.bucket()

	refs := make([]bucket.ObjectRef, 5)
	for i := range refs {
		key := fmt.Sprintf("many/%d.txt", i)
		s.put(ctx, key, strings.NewReader("x"))
		refs[i] = bucket.ObjectRef{Key: key}
	}

	result, err := store.DeleteMany(ctx, refs)

	s.Require().NoError(err)
	s.Len(result.Deleted, 5)
	s.Empty(result.Failed)
	s.Empty(result.Unsent)

	for _, ref := range refs {
		exists, err := store.Exists(ctx, ref.Key)
		s.Require().NoError(err)
		s.False(exists, "%s should be gone", ref.Key)
	}
}

func (s *BucketIntegrationTestSuite) Test_deleting_a_key_that_holds_nothing_is_not_a_failure() {
	// S3 answers a delete of something absent the same way it answers one that
	// was there, which is what makes a retry safe.
	ctx := context.Background()

	result, err := s.bucket().DeleteMany(ctx, []bucket.ObjectRef{{Key: "many/never-existed.txt"}})

	s.Require().NoError(err)
	s.Len(result.Deleted, 1)
	s.Empty(result.Failed)
}

func (s *BucketIntegrationTestSuite) bucket() bucket.Store {
	store, err := s.provider.Bucket(
		awstest.SimpleRef(resources.KindBucket, "ordersBucket", s.bucketName))
	s.Require().NoError(err)
	return store
}
