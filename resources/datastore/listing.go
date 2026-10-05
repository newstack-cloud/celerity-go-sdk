package datastore

import (
	"context"
	"iter"
)

// Items returns every item a query matches, fetching a page at a time.
//
//	for order, err := range datastore.Items[Order](ctx, orders, datastore.Query{
//	    Partition: customerID,
//	}) {
//	    if err != nil {
//	        return err
//	    }
//	    total += order.Total
//	}
//
// This is lazy, a page is fetched when the one before it has been read through, and
// stopping early stops the fetching, so a loop that breaks on the first match
// pays for one page rather than for the whole partition.
//
// A failure is yielded once, with the zero item, and ends the iteration.
//
// Where a handler is paging for a caller rather than reading to the end, it
// wants [Client.Query] itself, which returns one page and the cursor to hand
// back.
func Items[T any](ctx context.Context, store Client, q Query) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		for {
			var page []T
			cursor, err := store.Query(ctx, q, &page)
			if err != nil {
				yield(zero, err)
				return
			}

			for _, item := range page {
				if !yield(item, nil) {
					return
				}
			}

			if !cursor.More() {
				return
			}
			q.Cursor = string(cursor)
		}
	}
}

// Scanned returns every item in the store, fetching a page at a time.
//
//	for order, err := range datastore.Scanned[Order](ctx, orders, datastore.Scan{}) {
//	    if err != nil {
//	        return err
//	    }
//	    ...
//	}
//
// The scan equivalent of [Items], and lazy for the same reason. A scan reads
// every item in the store, so reading one through is the expensive thing this
// package can be asked to do; stopping early stops the fetching.
func Scanned[T any](ctx context.Context, store Client, s Scan) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		var zero T
		for {
			var page []T
			cursor, err := store.Scan(ctx, s, &page)
			if err != nil {
				yield(zero, err)
				return
			}

			for _, item := range page {
				if !yield(item, nil) {
					return
				}
			}

			if !cursor.More() {
				return
			}
			s.Cursor = string(cursor)
		}
	}
}
