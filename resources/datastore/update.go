package datastore

import "errors"

// MaxUpdates is the most mutations one update may carry.
//
// The ceiling is Cosmos DB's, whose patch takes ten operations, and it is
// enforced by every provider so that an update which works on one store is not
// refused on another. Exceeding it leads to [ErrTooManyOperations].
const MaxUpdates = 10

// ErrTooManyOperations is a request carrying more operations than the portable
// ceiling allows. Reported before a request is made, since a store that would
// have accepted it is the exception rather than the rule.
var ErrTooManyOperations = errors.New("celerity: too many operations in one request")

// ErrInvalidUpdate is a mutation that doesn't name any fields, or names one
// in an invalid format. Reported before a request is made.
var ErrInvalidUpdate = errors.New("celerity: invalid update")

// UpdateKind is which of the three supported mutations an [Update] is.
type UpdateKind string

const (
	UpdateSet       UpdateKind = "set"
	UpdateRemove    UpdateKind = "remove"
	UpdateIncrement UpdateKind = "increment"
)

// Update is one mutation of an item, built with [Set], [Remove] or [Increment].
//
// The fields are exported because a provider has to read them to translate,
// not because a caller should be filling them in.
type Update struct {
	Kind UpdateKind
	// Path names the field, with a dot between the segments of a nested one:
	// "status", or "profile.theme".
	//
	// Object fields only. An array element cannot be addressed, because
	// some backing stores (such as Firestore) cannot update, insert or delete
	// one by index at all, so a path that reached into an array
	// would work on some stores and not others.
	// To change an array, read the item and write it back with [IfUnchanged].
	//
	// A field whose own name contains a dot is not addressable for the same
	// reason a path is dotted: the two cannot be told apart.
	Path string
	// Value is what [Set] writes. Unused by the other two.
	Value any
	// By is what [Increment] adds. Unused by the other two.
	By float64
}

// Set writes a value at a field, creating the field if the item does not have
// it.
func Set(path string, value any) Update {
	return Update{Kind: UpdateSet, Path: path, Value: value}
}

// Remove deletes the field at a path. Removing a field that is not there is not
// an error.
func Remove(path string) Update {
	return Update{Kind: UpdateRemove, Path: path}
}

// Increment adds to the number at a path, and subtracts where the amount is
// negative. A field the item does not have counts as zero.
//
// Applied by the store, so two handlers incrementing the same field do not
// overwrite one another the way a read, an addition and a write would.
func Increment(path string, by float64) Update {
	return Update{Kind: UpdateIncrement, Path: path, By: by}
}
