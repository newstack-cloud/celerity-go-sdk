package celeritytest

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

// Bucket is an object store held in memory.
//
// Versioning is always on, which is the shape that can answer both kinds of
// test. A bucket without it is one whose keys each hold a single version, and
// the reads a handler makes are the same either way.
type Bucket struct {
	ref resources.Ref

	mu sync.Mutex
	// Versions per key, oldest first, so the current one is the last.
	objects map[string][]storedObject
	// Counted so that a version id is stable across runs, which a test
	// asserting on one needs.
	versions int
	now      func() time.Time
}

type storedObject struct {
	info   bucket.ObjectInfo
	body   []byte
	marker bool
}

// NewBucket returns an empty bucket.
func NewBucket(name string) *Bucket {
	return &Bucket{
		ref:     resources.Ref{Kind: resources.KindBucket, Name: name},
		objects: map[string][]storedObject{},
		now:     time.Now,
	}
}

// Len reports how many keys hold a current object, for a test asserting on
// what a handler left behind.
func (b *Bucket) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := 0
	for key := range b.objects {
		if _, ok := b.current(key); ok {
			count += 1
		}
	}
	return count
}

// Keys returns the keys holding a current object, in order.
func (b *Bucket) Keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	keys := make([]string, 0, len(b.objects))
	for key := range b.objects {
		if _, ok := b.current(key); ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// Contents returns what a key currently holds, and whether it holds anything.
func (b *Bucket) Contents(key string) ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	object, ok := b.current(key)
	if !ok {
		return nil, false
	}
	return bytes.Clone(object.body), true
}

// current is the newest version of a key that is not a deletion marker.
func (b *Bucket) current(key string) (storedObject, bool) {
	versions := b.objects[key]
	if len(versions) == 0 {
		return storedObject{}, false
	}
	newest := versions[len(versions)-1]
	if newest.marker {
		return storedObject{}, false
	}
	return newest, true
}

// version finds one named version, which may be a marker.
func (b *Bucket) version(key, id string) (storedObject, bool) {
	for _, object := range b.objects[key] {
		if object.info.VersionID == id {
			return object, true
		}
	}
	return storedObject{}, false
}

func (b *Bucket) Get(
	ctx context.Context, key string, opts ...bucket.GetOption,
) (*bucket.Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var options bucket.GetOptions
	for _, opt := range opts {
		opt(&options)
	}

	object, ok := b.find(key, options.VersionID)
	if !ok || object.marker {
		// A read of a marker is an absence, which is what the real stores
		// answer and what a caller passing an id back from a version listing
		// will hit.
		return nil, b.absent(key)
	}

	body, length := applyRange(object.body, options.Range)
	return &bucket.Object{
		Body:          io.NopCloser(bytes.NewReader(body)),
		Info:          object.info,
		ContentLength: length,
	}, nil
}

// Answers the part of an object a read asked for, clamped to what is
// there the way a store clamps it.
func applyRange(body []byte, r bucket.Range) ([]byte, int64) {
	if r.Whole() {
		return body, int64(len(body))
	}

	start := min(r.Start, int64(len(body)))
	end := int64(len(body))
	if r.Length > 0 {
		end = min(start+r.Length, end)
	}
	part := body[start:end]
	return part, int64(len(part))
}

func (b *Bucket) find(key, versionID string) (storedObject, bool) {
	if versionID == "" {
		versions := b.objects[key]
		if len(versions) == 0 {
			return storedObject{}, false
		}
		return versions[len(versions)-1], true
	}

	return b.version(key, versionID)
}

func (b *Bucket) Put(
	ctx context.Context, key string, body io.Reader, opts ...bucket.PutOption,
) (bucket.PutResult, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return bucket.PutResult{}, fmt.Errorf(
			"celerity: %s reading the body for %q: %w",
			b.ref, key, err,
		)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	var options bucket.PutOptions
	for _, opt := range opts {
		opt(&options)
	}

	b.versions += 1
	stored := storedObject{
		body: content,
		info: bucket.ObjectInfo{
			Key:          key,
			Size:         int64(len(content)),
			LastModified: b.now(),
			ETag:         fmt.Sprintf("%q", etagOf(content)),
			ContentType:  options.ContentType,
			Metadata:     options.Metadata,
			VersionID:    fmt.Sprintf("v%d", b.versions),
		},
	}
	b.objects[key] = append(b.objects[key], stored)
	return bucket.PutResult{
		ETag:      stored.info.ETag,
		VersionID: stored.info.VersionID,
	}, nil
}

func (b *Bucket) Delete(ctx context.Context, key string, opts ...bucket.DeleteOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var options bucket.DeleteOptions
	for _, opt := range opts {
		opt(&options)
	}
	b.delete(key, options.VersionID)
	return nil
}

// delete removes one version for good, or hides the key behind a marker.
//
// Deleting something that is not there is not a failure, which is what makes a
// delete worth retrying.
func (b *Bucket) delete(key, versionID string) {
	if versionID == "" {
		if _, ok := b.current(key); !ok {
			return
		}
		b.versions += 1
		b.objects[key] = append(b.objects[key], storedObject{
			marker: true,
			info: bucket.ObjectInfo{
				Key:          key,
				LastModified: b.now(),
				VersionID:    fmt.Sprintf("v%d", b.versions),
			},
		})
		return
	}

	kept := b.objects[key][:0]
	for _, object := range b.objects[key] {
		if object.info.VersionID != versionID {
			kept = append(kept, object)
		}
	}
	b.objects[key] = kept
}

func (b *Bucket) DeleteMany(
	ctx context.Context, refs []bucket.ObjectRef,
) (bucket.DeleteManyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := bucket.DeleteManyResult{
		Deleted: make([]bucket.ObjectRef, 0, len(refs)),
	}
	for _, ref := range refs {
		b.delete(ref.Key, ref.VersionID)
		result.Deleted = append(result.Deleted, ref)
	}
	return result, nil
}

func (b *Bucket) Info(
	ctx context.Context, key string, opts ...bucket.InfoOption,
) (bucket.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var options bucket.InfoOptions
	for _, opt := range opts {
		opt(&options)
	}

	object, ok := b.find(key, options.VersionID)
	if !ok || object.marker {
		return bucket.ObjectInfo{}, b.absent(key)
	}
	return object.info, nil
}

func (b *Bucket) Exists(
	ctx context.Context, key string, opts ...bucket.ExistsOption,
) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var options bucket.ExistsOptions
	for _, opt := range opts {
		opt(&options)
	}

	object, ok := b.find(key, options.VersionID)
	return ok && !object.marker, nil
}

func (b *Bucket) List(
	ctx context.Context, prefix string, opts ...bucket.ListOption,
) ([]bucket.ObjectInfo, bucket.Cursor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	options := listOptions(opts)
	var all []bucket.ObjectInfo
	for _, key := range b.sortedKeys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		if object, ok := b.current(key); ok {
			// A listing reports keys and sizes rather than each object's
			// headers, which is what the real stores answer with.
			all = append(all, bucket.ObjectInfo{
				Key:          object.info.Key,
				Size:         object.info.Size,
				LastModified: object.info.LastModified,
				ETag:         object.info.ETag,
			})
		}
	}

	page, cursor := paginate(all, options)
	return page, cursor, nil
}

func (b *Bucket) Versions(
	ctx context.Context, prefix string, opts ...bucket.ListOption,
) ([]bucket.ObjectVersion, bucket.Cursor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	options := listOptions(opts)

	var all []bucket.ObjectVersion
	for _, key := range b.sortedKeys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		versions := b.objects[key]
		// Newest first per key, which is the order a handler restoring
		// something reads: the version it wants is the first that is not a
		// marker.
		for i := len(versions) - 1; i >= 0; i-- {
			object := versions[i]
			all = append(all, bucket.ObjectVersion{
				Key:          key,
				VersionID:    object.info.VersionID,
				Size:         object.info.Size,
				LastModified: object.info.LastModified,
				ETag:         object.info.ETag,
				Current:      i == len(versions)-1,
				DeleteMarker: object.marker,
			})
		}
	}

	page, cursor := paginate(all, options)
	return page, cursor, nil
}

func (b *Bucket) Copy(
	ctx context.Context, sourceKey string, dest bucket.Destination, opts ...bucket.CopyOption,
) (bucket.CopyResult, error) {
	b.mu.Lock()
	source, ok := b.current(sourceKey)
	b.mu.Unlock()
	if !ok {
		return bucket.CopyResult{}, b.absent(sourceKey)
	}

	var options bucket.CopyOptions
	for _, opt := range opts {
		opt(&options)
	}

	target := b
	if dest.Store != nil {
		other, isTest := dest.Store.(*Bucket)
		if !isTest {
			return bucket.CopyResult{}, fmt.Errorf(
				"celerity: the copy destination is not a %T, so this bucket cannot "+
					"copy into it", b)
		}
		target = other
	}

	contentType, metadata := source.info.ContentType, source.info.Metadata
	if options.Replaces() {
		contentType, metadata = options.ContentType, options.Metadata
	}

	written, err := target.Put(ctx, dest.Key, bytes.NewReader(source.body),
		func(o *bucket.PutOptions) {
			o.ContentType = contentType
			o.Metadata = metadata
		})
	if err != nil {
		return bucket.CopyResult{}, err
	}
	return bucket.CopyResult(written), nil
}

func (b *Bucket) SignedURL(
	ctx context.Context, key string, action bucket.SignAction,
	expiry time.Duration, opts ...bucket.SignOption,
) (bucket.SignedURL, error) {
	// Nothing signs it: a URL from here is a stable, inspectable string, since
	// a test asserting that a handler handed one back has nothing to gain from
	// a real signature and would have to parse it to say anything.
	return bucket.SignedURL{
		URL:       fmt.Sprintf("https://celeritytest.invalid/%s/%s?action=%s", b.ref.Name, key, action),
		ExpiresAt: b.now().Add(expiry),
	}, nil
}

func (b *Bucket) sortedKeys() []string {
	keys := make([]string, 0, len(b.objects))
	for key := range b.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (b *Bucket) absent(key string) error {
	return fmt.Errorf("celerity: %s holds no object %q: %w", b.ref, key, bucket.ErrNotFound)
}

func listOptions(opts []bucket.ListOption) bucket.ListOptions {
	var options bucket.ListOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}

// Cuts a listing into the page a caller asked for, answering the
// cursor to resume from and an empty one at the end.
func paginate[T any](all []T, options bucket.ListOptions) ([]T, bucket.Cursor) {
	start := 0
	if options.Cursor != "" {
		// The cursor is the offset, which is opaque to a caller the way a real
		// store's is: nothing reads anything out of it.
		fmt.Sscanf(options.Cursor, "%d", &start)
	}

	start = min(start, len(all))

	end := len(all)
	if options.Limit > 0 {
		end = min(start+options.Limit, end)
	}

	if end < len(all) {
		return all[start:end], bucket.Cursor(fmt.Sprintf("%d", end))
	}

	return all[start:end], ""
}

// etagOf is a content hash in the shape an ETag takes, so that two writes of
// the same bytes compare equal the way they do on a real store.
func etagOf(content []byte) string {
	hash := fnv.New64a()
	_, _ = hash.Write(content)
	return fmt.Sprintf("%016x", hash.Sum64())
}
