package cache

import "time"

// KeyType is which of the five structures a key holds, as [Keys.Type] reports
// it.
type KeyType string

const (
	KeyString    KeyType = "string"
	KeyList      KeyType = "list"
	KeySet       KeyType = "set"
	KeySortedSet KeyType = "zset"
	KeyHash      KeyType = "hash"
)

// SetOptions is what a write can be given beyond the value.
type SetOptions struct {
	// TTL is how long the value lives. Zero stores it without a lifetime,
	// which is what a cache holding something evicted by pressure rather than
	// by age wants.
	TTL time.Duration
	// OnlyIfAbsent stores the value only where the key holds nothing, which
	// makes a write a claim: exactly one of several handlers racing to take a
	// lock or seed a value gets true.
	OnlyIfAbsent bool
	// OnlyIfPresent stores the value only where the key already holds one,
	// which refreshes something without creating it.
	OnlyIfPresent bool
}

// SetOption configures a write.
type SetOption func(*SetOptions)

// Expires gives a value a lifetime.
func Expires(ttl time.Duration) SetOption {
	return func(o *SetOptions) {
		o.TTL = ttl
	}
}

// IfNotExists writes only where the key holds nothing. See
// [SetOptions.OnlyIfAbsent].
func IfNotExists() SetOption {
	return func(o *SetOptions) {
		o.OnlyIfAbsent = true
	}
}

// IfExists writes only where the key already holds a value. See
// [SetOptions.OnlyIfPresent].
func IfExists() SetOption {
	return func(o *SetOptions) {
		o.OnlyIfPresent = true
	}
}

// ResolveSetOptions applies options in order, for a provider reading what a
// caller asked for.
func ResolveSetOptions(opts []SetOption) SetOptions {
	var resolved SetOptions
	for _, opt := range opts {
		opt(&resolved)
	}
	return resolved
}

// ScanOptions narrows a walk of the cache's keys.
type ScanOptions struct {
	// Match keeps only the keys matching a glob pattern, such as "session:*".
	// Empty keeps every key.
	//
	// Applied by the cache as it walks, so this reduces what crosses the
	// network. It does not reduce what the walk costs: every key is still
	// looked at.
	Match string
	// Count is how many keys the cache should look at per round trip. Zero
	// leaves it to the cache. A hint rather than a page size: a round trip may
	// yield more or fewer, including none.
	Count int64
	// Type keeps only the keys holding one structure. Empty keeps every kind.
	Type KeyType
}

// ScanOption configures a walk of the cache's keys.
type ScanOption func(*ScanOptions)

// Matching keeps only the keys matching a glob pattern.
func Matching(pattern string) ScanOption {
	return func(o *ScanOptions) {
		o.Match = pattern
	}
}

// PerRoundTrip hints how many keys the cache should look at at a time.
func PerRoundTrip(count int64) ScanOption {
	return func(o *ScanOptions) {
		o.Count = count
	}
}

// Holding keeps only the keys holding one structure.
func Holding(kind KeyType) ScanOption {
	return func(o *ScanOptions) {
		o.Type = kind
	}
}

// ResolveScanOptions applies options in order, for a provider reading what a
// caller asked for.
func ResolveScanOptions(opts []ScanOption) ScanOptions {
	var resolved ScanOptions
	for _, opt := range opts {
		opt(&resolved)
	}
	return resolved
}

// End is which end of a list an operation applies to.
type End string

const (
	// Right is the tail, which is where a push lands by default.
	Right End = "right"
	// Left is the head, which is where a pop takes from by default.
	Left End = "left"
)

// EndOptions is which end of a list an operation applies to.
type EndOptions struct {
	// End is the end to work from. Empty takes each operation's own default,
	// which is the tail for a push and the head for a pop: the two together
	// are a queue.
	End End
}

// EndOption configures which end of a list an operation applies to.
type EndOption func(*EndOptions)

// AtLeft works from the head of a list.
func AtLeft() EndOption {
	return func(o *EndOptions) {
		o.End = Left
	}
}

// AtRight works from the tail of a list.
func AtRight() EndOption {
	return func(o *EndOptions) {
		o.End = Right
	}
}

// ResolveEndOptions applies options in order, falling back to the end the
// operation defaults to.
func ResolveEndOptions(opts []EndOption, fallback End) End {
	resolved := EndOptions{End: fallback}
	for _, opt := range opts {
		opt(&resolved)
	}
	if resolved.End == "" {
		return fallback
	}
	return resolved.End
}

// SortedSetMember is a member of a sorted set and the score keeping it in
// order.
type SortedSetMember struct {
	Member string
	Score  float64
}

// ScoreRange is a band of scores, which may be open at either end.
//
// The bounds are inclusive. An open end is what a leaderboard read wants, where
// the question is "everything above this" and there is no ceiling to name.
type ScoreRange struct {
	// Min is the lowest score kept. Nil is open: everything down to the lowest
	// there is.
	Min *float64
	// Max is the highest score kept. Nil is open.
	Max *float64
}

// Scores is a closed band, from low to high inclusive.
func Scores(low, high float64) ScoreRange {
	return ScoreRange{Min: &low, Max: &high}
}

// ScoresFrom is a band open at the top: everything scoring at or above low.
func ScoresFrom(low float64) ScoreRange {
	return ScoreRange{Min: &low}
}

// ScoresUpTo is a band open at the bottom: everything scoring at or below high.
func ScoresUpTo(high float64) ScoreRange {
	return ScoreRange{Max: &high}
}

// AllScores is open at both ends, which is every member.
func AllScores() ScoreRange {
	return ScoreRange{}
}

// RangeOptions is how a read of a sorted set is ordered and narrowed.
type RangeOptions struct {
	// Descending reads from the highest score rather than the lowest, which
	// with a leaderboard is how the top is read without reading the whole of
	// it.
	Descending bool
	// Offset skips members from the start of the range. Only a score range
	// takes one; a rank range is already addressed by position.
	Offset int64
	// Limit is the most members returned. Zero returns all of them.
	Limit int64
}

// RangeOption configures a read of a sorted set.
type RangeOption func(*RangeOptions)

// Descending reads from the highest score rather than the lowest.
func Descending() RangeOption {
	return func(o *RangeOptions) {
		o.Descending = true
	}
}

// Page skips offset members and returns at most limit of them, within a score
// range.
func Page(offset, limit int64) RangeOption {
	return func(o *RangeOptions) {
		o.Offset, o.Limit = offset, limit
	}
}

// ResolveRangeOptions applies options in order, for a provider reading what a
// caller asked for.
func ResolveRangeOptions(opts []RangeOption) RangeOptions {
	var resolved RangeOptions
	for _, opt := range opts {
		opt(&resolved)
	}
	return resolved
}
