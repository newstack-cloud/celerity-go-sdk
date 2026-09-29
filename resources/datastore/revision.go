package datastore

// RevisionField is the item attribute a provider keeps an item's revision in,
// on a store that has no revision of its own.
//
// This is reserved, an application must not read, write or index it, and a schema must
// not declare it. Providers on a store that maintains its own version, such as
// Firestore's update time or Cosmos DB's ETag, do not use it.
const RevisionField = "_celerity_rev"

// Revision identifies the version of an item, so that a write can require that
// nothing has changed the item since it was read.
//
// This is opaque, what is in one belongs to the store that produced it. Revisions are
// not ordered and not comparable, and one is only ever handed back to the store
// it came from, for the item it was read from. Do not persist one, pass one
// between handlers, or use one obtained from a different data store.
//
// The zero Revision did not come from a read and is refused by [IfUnchanged].
// That is deliberate, as without it, a variable that was never assigned would turn
// a concurrency check into no check at all, which is the one failure mode that
// would never be noticed.
type Revision struct {
	value string
	known bool
}

// Unrevisioned is the revision of an item that carries none.
//
// An item written before the application adopted a Celerity SDK, or by a data
// script, a console edit or another service, has no stored revision. A read of
// one returns this, and it is used like any other revision:
//
//	var order Order
//	rev, err := orders.Get(ctx, key, &order)
//	// ...
//	_, err = orders.Put(ctx, key, order, resources.IfUnchanged(rev))
//
// [IfUnchanged] with it requires that the item still carries no revision, so
// read-modify-write is correct on items that predate revisions without any
// migration or back-fill. Every write sets a revision, so an item passes
// through this state at most once.
var Unrevisioned = Revision{known: true}

// NewRevision is how a provider reports the revision it read off an item. The
// empty string means the item carried none, which is [Unrevisioned].
func NewRevision(value string) Revision {
	return Revision{value: value, known: true}
}

// Value reports the stored revision, and whether the item carried one at all.
// For a provider translating a revision into its store's own precondition.
func (r Revision) Value() (string, bool) {
	return r.value, r.value != ""
}

// Known reports whether the revision came from a read. False for the zero
// value, which no write accepts.
func (r Revision) Known() bool {
	return r.known
}
