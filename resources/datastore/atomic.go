package datastore

import "errors"

// MaxAtomicOps is the most operations one atomic write may carry.
//
// Enforced by every provider, so a write that works on one store is not
// refused on another. Exceeding it is [ErrTooManyOperations].
const MaxAtomicOps = 100

// ErrNotSupported is a capability the configured store does not have.
//
// Rare, since the contract is the intersection of what these stores can do.
// [Client.Atomically] reports it on a store with single-item conditional writes
// and no multi-item transactions, such as ScyllaDB Alternator.
var ErrNotSupported = errors.New("celerity: not supported by this data store")

// ErrWrongPartition is an operation in an atomic write addressing a partition
// other than the one the write named. Reported before a request is made.
var ErrWrongPartition = errors.New("celerity: operation outside the write's partition")

type atomicKind uint8

const (
	atomicPut atomicKind = iota
	atomicUpdate
	atomicDelete
)

// AtomicOp is one operation in an atomic write, built with [AtomicPut],
// [AtomicUpdate] or [AtomicDelete].
//
// Unlike a [BatchOp] it carries preconditions, which is why the two are
// separate types. The exported fields are there for a provider translating one
// to read.
type AtomicOp struct {
	// Key addresses the item the operation applies to.
	Key Key
	// Item is what a put writes.
	Item any
	// Updates are the mutations an update applies.
	Updates []Update
	// Options are the preconditions the operation is subject to.
	Options WriteOptions

	kind atomicKind
}

// AtomicPut writes a whole item as part of an atomic write.
func AtomicPut(key Key, item any, opts ...WriteOption) AtomicOp {
	return AtomicOp{
		kind:    atomicPut,
		Key:     key,
		Item:    item,
		Options: ResolveWriteOptions(opts),
	}
}

// AtomicUpdate mutates part of an item as part of an atomic write. As with
// [Client.Update], the item has to already exist.
func AtomicUpdate(key Key, updates []Update, opts ...WriteOption) AtomicOp {
	return AtomicOp{
		kind:    atomicUpdate,
		Key:     key,
		Updates: updates,
		Options: ResolveWriteOptions(opts),
	}
}

// AtomicDelete removes an item as part of an atomic write.
func AtomicDelete(key Key, opts ...WriteOption) AtomicOp {
	return AtomicOp{
		kind:    atomicDelete,
		Key:     key,
		Options: ResolveWriteOptions(opts),
	}
}

// IsPut reports whether the operation writes a whole item.
func (o AtomicOp) IsPut() bool {
	return o.kind == atomicPut
}

// IsUpdate reports whether the operation mutates part of an item.
func (o AtomicOp) IsUpdate() bool {
	return o.kind == atomicUpdate
}

// IsDelete reports whether the operation removes an item.
func (o AtomicOp) IsDelete() bool {
	return o.kind == atomicDelete
}
