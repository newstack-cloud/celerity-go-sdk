package cache

import "time"

// Tx queues the writes of a transaction.
//
// Every method returns the same Tx, so a group reads as one statement:
//
//	results, err := sessions.Transaction(ctx, func(tx cache.Tx) {
//		tx.Set("{user:1}:name", "Ada").
//			Increment("{user:1}:visits", 1).
//			Expire("{user:1}:name", time.Hour)
//	})
//
// Writes only. The cache answers a queued command once the whole group has been
// applied, so there is nothing to read here: a value read inside would be one
// from before the transaction began. Where a decision has to be made on what
// the cache currently holds, read before the transaction and guard the write
// with [IfNotExists] or [IfExists], or take the connection details from the
// provider and use the cache's own check-and-set.
//
// Nothing here returns an error. A command is not sent as it is queued, so
// there is nothing yet to fail; what the group answers, including a command
// that the cache refused, comes back from [Transactor.Transaction].
type Tx interface {
	// Set queues a write. See [Strings.Set].
	Set(key, value string, opts ...SetOption) Tx
	// Delete queues a removal. See [Strings.Delete].
	Delete(key string) Tx
	// GetSet queues a write that answers with the value it replaced. See
	// [Strings.GetSet].
	GetSet(key, value string) Tx
	// Append queues an append. See [Strings.Append].
	Append(key, value string) Tx

	// Increment queues an addition to a counter. See [Counters.Increment].
	Increment(key string, delta int64) Tx
	// Decrement queues a subtraction from a counter. See [Counters.Decrement].
	Decrement(key string, delta int64) Tx
	// IncrementFloat queues an addition to a counter that is not whole. See
	// [Counters.IncrementFloat].
	IncrementFloat(key string, delta float64) Tx

	// HashSet queues writes to a hash's fields. See [Hashes.HashSet].
	HashSet(key string, fields map[string]string) Tx
	// HashDelete queues removals of a hash's fields. See [Hashes.HashDelete].
	HashDelete(key string, fields []string) Tx
	// HashIncrement queues an addition to a hash field. See
	// [Hashes.HashIncrement].
	HashIncrement(key, field string, delta int64) Tx

	// ListPush queues a push onto a list. See [Lists.ListPush].
	ListPush(key string, values []string, opts ...EndOption) Tx
	// ListTrim queues a trim of a list. See [Lists.ListTrim].
	ListTrim(key string, start, stop int64) Tx

	// SetAdd queues additions to a set. See [Sets.SetAdd].
	SetAdd(key string, members []string) Tx
	// SetRemove queues removals from a set. See [Sets.SetRemove].
	SetRemove(key string, members []string) Tx

	// SortedSetAdd queues additions to a sorted set. See
	// [SortedSets.SortedSetAdd].
	SortedSetAdd(key string, members []SortedSetMember) Tx
	// SortedSetRemove queues removals from a sorted set. See
	// [SortedSets.SortedSetRemove].
	SortedSetRemove(key string, members []string) Tx
	// SortedSetIncrement queues an addition to a member's score. See
	// [SortedSets.SortedSetIncrement].
	SortedSetIncrement(key, member string, delta float64) Tx

	// Expire queues a lifetime for a key. See [Keys.Expire].
	Expire(key string, ttl time.Duration) Tx
	// Persist queues the removal of a key's lifetime. See [Keys.Persist].
	Persist(key string) Tx
	// Rename queues a move to another key. See [Keys.Rename].
	Rename(key, newKey string) Tx
}
