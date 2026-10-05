// The cache package contains the provider-agnostic interface to a key-value cache:
// ElastiCache, Memorystore, Azure Cache, and the Valkey instance a local development
// session runs.
//
// A handle is taken by naming the blueprint resource:
//
//	sessions := resources.Cache(app, "sessionCache")
//
// The five data structures every managed cache offers are here: strings,
// hashes, lists, sets and sorted sets, along with key management, counters and
// transactions. [Client] is assembled from one interface per group, so that the
// group a call belongs to is visible where it is declared.
//
// Implementations are per provider and live in their own modules, such as
// resources/redis.
package cache

import (
	"context"
	"errors"
	"iter"
	"time"
)

// ErrNotFound reports that the cache holds nothing under the key asked for,
// which for a cache is an ordinary answer rather than a failure.
//
//	if errors.Is(err, cache.ErrNotFound) { ... }
//
// A read that can legitimately come back empty, such as
// [Hashes.HashGetAll] or [Lists.ListRange], returns an empty collection rather
// than this, because a collection can carry the absence itself.
var ErrNotFound = errors.New("celerity: not found")

// ErrCrossSlot reports an operation whose keys do not all live on the same
// shard of a clustered cache, which the cache cannot apply as one command.
//
// Refused locally rather than sent, since the server answers with a hash slot
// number rather than the keys that disagreed. Co-locate the keys with a hash
// tag, the part of a key in braces: {user:123}:name and {user:123}:prefs share
// the tag user:123 and are guaranteed to land on the same shard.
var ErrCrossSlot = errors.New("celerity: keys are not on one shard")

// Client is a key-value cache (e.g. ElastiCache, Memorystore, Azure Cache).
//
// Every single-key operation works the same whether the cache is one instance
// or a cluster; the implementation routes it. What differs is the multi-key
// operations, and each says what it does when the keys span shards.
type Client interface {
	Strings
	Keys
	Counters
	Hashes
	Lists
	Sets
	SortedSets
	Transactor
	Connector
}

// Strings is the plain key-to-value cache, and the batch reads and writes over
// it.
type Strings interface {
	// Get reads a value, reporting [ErrNotFound] where the cache holds none.
	Get(ctx context.Context, key string) (string, error)
	// Set stores a value, reporting whether it was stored.
	//
	// False is not a failure: it is the answer to a [IfNotExists] or
	// [IfExists] that did not hold, which is how a caller learns it lost a
	// race to set a value.
	Set(ctx context.Context, key, value string, opts ...SetOption) (bool, error)
	// Delete removes a key, reporting whether one was there. Deleting nothing
	// is not a failure.
	Delete(ctx context.Context, key string) (bool, error)
	// GetSet stores a value and returns the one it replaced, reporting
	// [ErrNotFound] where there was none.
	//
	// Atomic, so another handler never sees the key without a value.
	GetSet(ctx context.Context, key, value string) (string, error)
	// Append adds to the end of a value and returns the length of the result,
	// creating the key where it holds nothing.
	Append(ctx context.Context, key, value string) (int64, error)

	// GetMany reads many keys in one request, omitting those the cache holds
	// nothing under.
	//
	// A map rather than a list in key order, since an empty string is a value a
	// cache can legitimately hold and could not be told from an absence. The
	// keys are the caller's own, so the order is theirs to restore.
	//
	// On a cluster the keys are read shard by shard and the results merged,
	// which is safe because a batch read is not atomic on any cache.
	GetMany(ctx context.Context, keys []string) (map[string]string, error)
	// SetMany stores many values in one request.
	//
	// On a cluster the writes are grouped by shard and each group applied
	// atomically. Across shards they are not: one group may be applied and
	// another refused. Where all-or-none matters, co-locate the keys with a
	// hash tag and use [Transactor.Transaction].
	SetMany(ctx context.Context, values map[string]string) error
	// DeleteMany removes many keys in one request and reports how many were
	// there. Grouped by shard the way [Strings.SetMany] is.
	DeleteMany(ctx context.Context, keys []string) (int64, error)
}

// Keys is what can be asked and done about a key itself rather than its value.
type Keys interface {
	// Exists reports whether the cache holds anything under a key.
	Exists(ctx context.Context, key string) (bool, error)
	// TTL reports how long a key has left, and whether it expires at all.
	//
	// A key with no expiry returns false and no duration; a key that is not
	// there reports [ErrNotFound].
	TTL(ctx context.Context, key string) (ttl time.Duration, expires bool, err error)
	// Expire gives a key a lifetime, reporting whether one was set. False means
	// the key was not there.
	Expire(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Persist takes a key's lifetime away, reporting whether one was removed.
	// False means the key was not there or had no expiry.
	Persist(ctx context.Context, key string) (bool, error)
	// Type reports which of the five structures a key holds, reporting
	// [ErrNotFound] where it holds nothing.
	Type(ctx context.Context, key string) (KeyType, error)
	// Rename moves a value to another key, reporting [ErrNotFound] where the
	// source holds nothing.
	//
	// On a cluster both keys have to be on one shard, since the cache cannot
	// move a value between them; keys that are not report [ErrCrossSlot].
	Rename(ctx context.Context, key, newKey string) error
	// Scan walks the keys of the cache, a page at a time, yielding each key and
	// stopping at the first error.
	//
	// An iterator rather than a page and a cursor: a cache cursor is only valid
	// against the cache's current contents, so it is not something to hand to a
	// client and resume a later request with.
	//
	// The cache is being written to while this runs, so a key may be missed or
	// seen twice. On a cluster every shard is walked and the results merged, so
	// the order is not the cache's own.
	Scan(ctx context.Context, opts ...ScanOption) iter.Seq2[string, error]
}

// Counters is the arithmetic a cache does on the server, so that two handlers
// counting the same thing do not lose a count between them.
type Counters interface {
	// Increment adds to a counter and returns what it became, treating a key
	// that holds nothing as zero.
	//
	// Atomic on the server, so overlapping invocations never lose a count the
	// way a read, an add and a write from a handler would.
	Increment(ctx context.Context, key string, delta int64) (int64, error)
	// Decrement subtracts from a counter and returns what it became.
	Decrement(ctx context.Context, key string, delta int64) (int64, error)
	// IncrementFloat adds to a counter that is not whole. Negative to subtract.
	IncrementFloat(ctx context.Context, key string, delta float64) (float64, error)
}

// Hashes store fields under one key, for a structured value that can be read
// and written a field at a time rather than serialised whole.
type Hashes interface {
	// HashGet reads one field, reporting [ErrNotFound] where the key or the
	// field holds nothing.
	HashGet(ctx context.Context, key, field string) (string, error)
	// HashSet writes fields, creating the hash where it does not exist and
	// leaving fields it does not name alone.
	HashSet(ctx context.Context, key string, fields map[string]string) error
	// HashDelete removes fields and reports how many were there.
	HashDelete(ctx context.Context, key string, fields []string) (int64, error)
	// HashGetAll reads every field. An empty map where the key holds nothing.
	HashGetAll(ctx context.Context, key string) (map[string]string, error)
	// HashExists reports whether a field is there.
	HashExists(ctx context.Context, key, field string) (bool, error)
	// HashIncrement adds to a field and returns what it became, treating a
	// field that is not there as zero.
	HashIncrement(ctx context.Context, key, field string, delta int64) (int64, error)
	// HashKeys reads the field names. An empty slice where the key holds
	// nothing.
	HashKeys(ctx context.Context, key string) ([]string, error)
	// HashLen reports how many fields a hash has. Zero where the key holds
	// nothing.
	HashLen(ctx context.Context, key string) (int64, error)
}

// Lists are ordered and can be pushed and popped at either end, which is what
// makes them a queue, a recent-activity feed or a bounded buffer.
type Lists interface {
	// ListPush adds values to one end of a list and returns its new length,
	// creating the list where it does not exist.
	ListPush(ctx context.Context, key string, values []string, opts ...EndOption) (int64, error)
	// ListPop removes and returns values from one end of a list. An empty slice
	// where the list holds nothing, since a pop that found nothing is an
	// ordinary answer rather than [ErrNotFound].
	ListPop(ctx context.Context, key string, count int64, opts ...EndOption) ([]string, error)
	// ListRange reads a range of a list without changing it, where zero is the
	// first element and -1 the last.
	ListRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	// ListLen reports a list's length. Zero where the key holds nothing.
	ListLen(ctx context.Context, key string) (int64, error)
	// ListTrim discards everything outside a range.
	ListTrim(ctx context.Context, key string, start, stop int64) error
	// ListIndex reads one element, reporting [ErrNotFound] where the index is
	// outside the list or the key holds nothing.
	ListIndex(ctx context.Context, key string, index int64) (string, error)
}

// Sets hold unique members in no order, for tagging, membership and counting
// something once however many times it happens.
type Sets interface {
	// SetAdd adds members and reports how many were new.
	SetAdd(ctx context.Context, key string, members []string) (int64, error)
	// SetRemove removes members and reports how many were there.
	SetRemove(ctx context.Context, key string, members []string) (int64, error)
	// SetMembers reads every member. An empty slice where the key holds
	// nothing.
	SetMembers(ctx context.Context, key string) ([]string, error)
	// SetIsMember reports whether a member is in a set.
	SetIsMember(ctx context.Context, key, member string) (bool, error)
	// SetLen reports how many members a set has. Zero where the key holds
	// nothing.
	SetLen(ctx context.Context, key string) (int64, error)
	// SetUnion returns the members of every set named, without storing the
	// result.
	//
	// On a cluster the keys have to be on one shard, since the cache computes
	// this itself; keys that are not report [ErrCrossSlot].
	SetUnion(ctx context.Context, keys []string) ([]string, error)
	// SetIntersect returns the members every set named has, without storing the
	// result. One shard, as [Sets.SetUnion] is.
	SetIntersect(ctx context.Context, keys []string) ([]string, error)
	// SetDiff returns the members of the first set that are in none of the
	// rest, without storing the result. One shard, as [Sets.SetUnion] is.
	SetDiff(ctx context.Context, keys []string) ([]string, error)
}

// SortedSets hold unique members each carrying a score, kept in score order,
// which is what makes them a leaderboard, a priority queue, a time index or the
// window of a rate limiter.
type SortedSets interface {
	// SortedSetAdd adds members, or moves the score of ones already there, and
	// reports how many were new.
	SortedSetAdd(ctx context.Context, key string, members []SortedSetMember) (int64, error)
	// SortedSetRemove removes members and reports how many were there.
	SortedSetRemove(ctx context.Context, key string, members []string) (int64, error)
	// SortedSetScore reads a member's score, reporting [ErrNotFound] where the
	// member or the key holds nothing.
	SortedSetScore(ctx context.Context, key, member string) (float64, error)
	// SortedSetRank reads a member's position in score order, counting from
	// zero, and reports [ErrNotFound] where the member is not there.
	SortedSetRank(ctx context.Context, key, member string, opts ...RangeOption) (int64, error)
	// SortedSetRange reads members by position, where zero is the
	// lowest-scoring and -1 the highest.
	//
	// Always with scores.
	SortedSetRange(
		ctx context.Context, key string, start, stop int64, opts ...RangeOption,
	) ([]SortedSetMember, error)
	// SortedSetRangeByScore reads the members scoring within a range. See
	// [ScoreRange] for the bounds, which may be open.
	SortedSetRangeByScore(
		ctx context.Context, key string, scores ScoreRange, opts ...RangeOption,
	) ([]SortedSetMember, error)
	// SortedSetIncrement adds to a member's score and returns what it became,
	// adding the member where it is not there.
	SortedSetIncrement(ctx context.Context, key, member string, delta float64) (float64, error)
	// SortedSetLen reports how many members a sorted set has. Zero where the
	// key holds nothing.
	SortedSetLen(ctx context.Context, key string) (int64, error)
	// SortedSetCountByScore reports how many members score within a range,
	// without reading them.
	SortedSetCountByScore(ctx context.Context, key string, scores ScoreRange) (int64, error)
	// SortedSetRemoveByRank removes the members within a position range and
	// reports how many were removed.
	SortedSetRemoveByRank(ctx context.Context, key string, start, stop int64) (int64, error)
	// SortedSetRemoveByScore removes the members scoring within a range and
	// reports how many were removed.
	SortedSetRemoveByScore(ctx context.Context, key string, scores ScoreRange) (int64, error)
}

// Transactor applies a group of writes as one.
type Transactor interface {
	// Transaction applies every write queued by the function, or none of them,
	// and returns what each one answered in the order they were queued.
	//
	// The writes are queued rather than sent as the function runs, and the
	// cache applies them with nothing else in between. There is nothing to read
	// inside: the cache answers a queued command only once the whole group has
	// been applied, so a value read in the function would be one from before
	// the transaction began.
	//
	// On a cluster every key has to be on one shard; keys that are not report
	// [ErrCrossSlot] before anything is sent.
	Transaction(ctx context.Context, queue func(Tx)) ([]any, error)
}

// Connector hands back the details of the connection a cache makes, for an
// application building a client of its own.
//
// This is how the capabilities the contract leaves out are reached, for example,
// pub/sub, streams, check-and-set through WATCH, scripting, pipelining, geo commands,
// HyperLogLog and bitmaps.
//
// Using it means application code may need changing when a deployment switches
// between one instance and a cluster, since a cluster needs a cluster-aware
// client. The operations above route themselves; a client built by hand does
// not.
type Connector interface {
	// Connection reads what the deployment recorded about the cache, resolving
	// a password held by reference. It makes no connection itself.
	Connection(ctx context.Context) (Connection, error)
}

// Connection is what a cache is reached with.
type Connection struct {
	// Host is the endpoint, which for a sharded cluster is the configuration
	// endpoint rather than any one shard.
	Host string
	// Port the cache listens on.
	Port int
	// TLS reports whether the connection is encrypted. Always true where
	// AuthMode is [AuthIAM], since a signed token is a credential in its own
	// right and has to travel over something nobody can read.
	TLS bool
	// ClusterMode reports whether the cache is sharded, which decides whether a
	// client has to follow the redirect a command for another shard comes back
	// as.
	ClusterMode bool
	// User the connection authenticates as, where the platform has one. Empty
	// where it does not.
	User string
	// AuthMode is [AuthPassword] or [AuthIAM].
	AuthMode string
	// KeyPrefix every key of this application is under.
	//
	// A local development session puts several applications on one cache and
	// keeps them apart with this. A client built by hand has to apply it, since
	// nothing else will: the operations above apply it for themselves.
	KeyPrefix string
	// Password produces the password to connect with, and is never nil.
	//
	// A function rather than a value because a cache reached with the platform's
	// own identity signs a fresh token per connection, which is shorter-lived
	// than the environment holding the pool: call it per connection rather than
	// holding on to what it returned. Returns the recorded password where the
	// deployment has one, and the empty string where it has none, as a local
	// session does.
	Password func(context.Context) (string, error)
}

const (
	// AuthPassword is a cache reached with a password the deployment recorded,
	// either written down or held in the platform's secret store.
	AuthPassword = "password"
	// AuthIAM is a cache reached with the platform's own identity, signing a
	// short-lived token per connection.
	AuthIAM = "iam"
)
