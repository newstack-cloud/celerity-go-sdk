// Package datastore is the provider-agnostic interface to a NoSQL document
// store: DynamoDB, Firestore, Cosmos DB, and the engines a self-hosted
// deployment runs.
//
// A handle is taken by naming the blueprint resource, and the vocabulary for
// addressing, narrowing and conditioning a write lives here:
//
//	orders := resources.Datastore(app, "ordersTable")
//	rev, err := orders.Get(ctx, datastore.Key{Partition: customerID, Sort: orderID}, &order)
//
// Implementations are per provider and live in their own modules, such as
// resources/aws.
package datastore

import (
	"context"
	"errors"
)

// ErrNotFound reports that the store doesn't hold an item under the key asked for.
//
// Providers translate to this one and a handler checks for it with errors.Is:
//
//	if errors.Is(err, datastore.ErrNotFound) { ... }
//
// Not used by a delete, which is idempotent on every backing store: a key that
// holds nothing is not a failure.
var ErrNotFound = errors.New("celerity: not found")

// ErrConditionFailed reports that a conditional write was refused because the
// item was not in the state the condition described.
//
// Often an answer rather than a failure, it is what a handler doing a read, a
// decision and a write is told when something else got there first, and the
// ordinary response is to read again and retry.
//
//	if errors.Is(err, datastore.ErrConditionFailed) { ... }
var ErrConditionFailed = errors.New("celerity: condition not met")

// ErrInvalidRevision is a revision that did not come from a read, passed to
// [IfUnchanged]. Reported before a request is made, so no write happens without
// the precondition that was asked for.
var ErrInvalidRevision = errors.New("celerity: revision did not come from a read")

// ErrInvalidCursor is a cursor the store did not produce, passed to
// [Query.Cursor] or [Scan.Cursor].
//
// A cursor travels out to a client and back, so this reports what a client sent
// rather than something the application got wrong: a handler mapping errors to
// a response should answer it as it answers any other bad request.
//
//	if errors.Is(err, datastore.ErrInvalidCursor) { ... }
//
// Reported whether the cursor is malformed or merely does not belong to the
// query it was given to.
var ErrInvalidCursor = errors.New("celerity: cursor did not come from this store")

// Client is a NoSQL document store (e.g. DynamoDB, Firestore, Cosmos DB).
type Client interface {
	// Get fills out with the item under the key and returns its revision, for
	// a later write that must require the item has not changed since.
	Get(ctx context.Context, key Key, out any) (Revision, error)
	// Put writes an item, replacing whatever was under the key.
	//
	// [If] makes it conditional, which a handler that read, decided and is
	// writing back needs. Without one, two invocations running at the same time
	// silently lose an update.
	//
	// Returns the item's new revision, so a sequence of writes needs no read
	// between them.
	Put(ctx context.Context, key Key, item any, opts ...WriteOption) (Revision, error)
	// Delete removes an item, and is idempotent. [If] makes it conditional.
	Delete(ctx context.Context, key Key, opts ...WriteOption) error
	// Query fills out with one page of the items under a partition, and
	// returns the cursor to resume from. Use [Items] to read a query to the
	// end.
	Query(ctx context.Context, q Query, out any) (Cursor, error)
	// Scan fills out with one page of the whole store, and returns the cursor
	// to resume from. Use [Scanned] to read a scan to the end.
	//
	// A scan reads every item; a query reads one partition. Reach for this only
	// for genuinely whole-store work, such as an export or a backfill.
	Scan(ctx context.Context, s Scan, out any) (Cursor, error)
	// BatchGet fills out with the items under the keys, in as few requests as
	// the store allows, and returns the keys it could not fetch.
	//
	// Items come back in no particular order, and a key that holds nothing is
	// simply absent rather than an error, so match items to keys by key and
	// never by position.
	BatchGet(ctx context.Context, keys []Key, out any) ([]Key, error)
	// Update mutates parts of an item that already exists, leaving the rest
	// alone, and returns the item's new revision.
	//
	// Built with [Set], [Remove] and [Increment], at most [MaxUpdates] of them.
	// The item has to be there: an update of a key that holds nothing reports
	// [ErrNotFound] rather than creating it. Use [Client.Put] to create.
	//
	// Preconditions apply as they do to a put, so an update can be partial and
	// conditional in the same request.
	Update(ctx context.Context, key Key, updates []Update, opts ...WriteOption) (Revision, error)
	// BatchWrite applies puts and deletes, in as few requests as the store
	// allows, and returns the operations it could not apply.
	//
	// This is not atomic. Some operations may succeed while others fail, on every
	// store, so a caller that needs all or none cannot build it out of this.
	//
	// This only supports Puts and deletes. A batch write does not carry update on the stores this
	// has to work on. To change part of each of many items, call [Client.Update]
	// per item, or [Client.Atomically] where they share a partition and
	// all-or-none is wanted as well.
	//
	// A [BatchOp] carries no preconditions, for the same reason: no store takes
	// one on a batch write.
	BatchWrite(ctx context.Context, ops []BatchOp) ([]BatchOp, error)
	// Atomically applies every operation or none of them.
	//
	// Built with [AtomicPut], [AtomicUpdate] and [AtomicDelete], at most
	// [MaxAtomicOps] of them, each able to carry its own preconditions. A
	// precondition that does not hold refuses the whole write with
	// [ErrConditionFailed] and nothing is applied.
	//
	// Every operation has to address the partition named here, which is a
	// parameter rather than inferred from the operations so that the
	// one-partition rule cannot be broken by accident.
	//
	// There is no reading inside. For read-modify-write, take the revision from
	// an earlier read and pass it as [IfUnchanged] on the operation that needs
	// it.
	//
	// Reports [ErrNotSupported] on a store with no multi-item transactions.
	Atomically(ctx context.Context, partition string, ops []AtomicOp) error
}

// BatchOp is one operation in a batch write, built with [PutOp] or [DeleteOp].
//
// The exported fields are there for a provider translating one to read. Which
// of the two an operation is cannot be set from outside.
type BatchOp struct {
	// Key addresses the item the operation applies to.
	Key Key
	// Item is what a put writes, and is nil for a delete.
	Item any

	remove bool
}

// PutOp writes an item as part of a batch, replacing whatever was under the key.
func PutOp(key Key, item any) BatchOp {
	return BatchOp{Key: key, Item: item}
}

// DeleteOp removes an item as part of a batch.
func DeleteOp(key Key) BatchOp {
	return BatchOp{Key: key, remove: true}
}

// IsDelete reports which of the two an operation is, for a provider translating
// one.
func (o BatchOp) IsDelete() bool {
	return o.remove
}

// Scan describes a read of the whole data store.
//
// Not a [Query] without a partition: a scan does not take a sort condition, since
// there is no partition to narrow, and doesn't name a index. What is left is a
// filter, a projection and where to resume.
type Scan struct {
	// Filter drops items that do not match a condition on their own fields.
	// Nil applies none.
	//
	// Applied by the store after the items are read, so every item is still
	// read and paid for. A filter makes a scan easier to receive, but not
	// cheaper to run.
	Filter *Condition
	// Project lists the fields to return. Empty returns whole items. The key
	// fields are always returned whether or not they are listed.
	Project []string
	// Limit is the most items one page holds. Zero leaves it to the store.
	Limit int
	// Cursor resumes a scan where an earlier one stopped. See [Cursor].
	Cursor string
}

// Cursor is a position in a listing, to resume it from.
//
// Thi is opaque, what is in one belongs to the store that produced it, and only that
// store can read it back. A string, so a handler can hand it to a client as the
// token for the next page and take it back on the next request. Empty means a
// listing reached its end.
type Cursor string

// More reports whether a listing has more pages of items to retrieve.
func (c Cursor) More() bool {
	return c != ""
}

// Key addresses one item in a data store.
type Key struct {
	Partition string
	Sort      string
}

// Query describes a data store query.
type Query struct {
	// Partition is the value every matching item shares.
	Partition string
	// Sort narrows the query to part of the partition. Nil reads all of it.
	//
	// Built with [SortBetween] and the other Sort functions. This is what the
	// store uses to read less, rather than reading the partition and discarding
	// what does not match, so a partition that grows can still be queried
	// efficiently.
	Sort *SortCondition
	// Filter narrows the page to items matching a condition on their own
	// fields. Nil applies none.
	//
	// Applied by the store after the items are read, so it reduces what crosses
	// the network and not what the query costs. [Query.Sort] is what reduces the
	// cost, so prefer it where the data model allows.
	Filter *Condition
	// Index names a secondary index to query rather than the table itself.
	Index string
	// Project lists the fields to return. Empty returns whole items.
	//
	// The key fields are always returned whether or not they are listed. Like a
	// filter, this reduces what crosses the network rather than what the query
	// costs.
	Project []string
	// Descending reads the partition from the end of the sort order rather than
	// the start, which with a timestamp sort key is how the newest items are
	// read without reading the partition through.
	Descending bool
	// Limit is the most items one page holds. Zero leaves it to the store.
	Limit int
	// Cursor resumes a query where an earlier one stopped. See [Cursor].
	Cursor string
}
