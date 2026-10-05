package bucket

import (
	"context"
	"iter"
)

// Objects returns every object under a prefix, fetching a page at a time.
//
//	for object, err := range bucket.Objects(ctx, uploads, "invoices/") {
//	    if err != nil {
//	        return err
//	    }
//	    ...
//	}
func Objects(
	ctx context.Context, store Store, prefix string, opts ...ListOption,
) iter.Seq2[ObjectInfo, error] {
	return func(yield func(ObjectInfo, error) bool) {
		for {
			page, cursor, err := store.List(ctx, prefix, opts...)
			if err != nil {
				yield(ObjectInfo{}, err)
				return
			}

			for _, object := range page {
				if !yield(object, nil) {
					return
				}
			}

			if !cursor.More() {
				return
			}
			opts = append(opts, resumeAt(cursor))
		}
	}
}

// This is applied after the caller's own options, so the position this
// listing reached wins over one the caller started from.
func resumeAt(cursor Cursor) ListOption {
	return func(o *ListOptions) {
		o.Cursor = string(cursor)
	}
}

// AllVersions returns every version of every object under a prefix, fetching a
// page at a time.
//
//	for version, err := range bucket.AllVersions(ctx, uploads, "invoices/") {
//	    if err != nil {
//	        return err
//	    }
//	    if version.DeleteMarker {
//	        continue
//	    }
//	    ...
//	}
func AllVersions(
	ctx context.Context, store Store, prefix string, opts ...ListOption,
) iter.Seq2[ObjectVersion, error] {
	return func(yield func(ObjectVersion, error) bool) {
		for {
			page, cursor, err := store.Versions(ctx, prefix, opts...)
			if err != nil {
				yield(ObjectVersion{}, err)
				return
			}

			for _, version := range page {
				if !yield(version, nil) {
					return
				}
			}

			if !cursor.More() {
				return
			}
			opts = append(opts, resumeAt(cursor))
		}
	}
}
