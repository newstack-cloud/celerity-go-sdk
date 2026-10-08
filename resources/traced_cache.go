package resources

import (
	"context"
	"iter"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

type tracedCache struct {
	inner cache.Client
	ref   Ref
}

// What every cache span records including which resource, and the key the
// operation is about where it has one.
func (c tracedCache) attrs(key string) []telemetry.Attr {
	if key == "" {
		return resourceAttrs(c.ref, "cache")
	}
	return resourceAttrs(c.ref, "cache", telemetry.String("cache.key", key))
}

// Scan walks the cache a page at a time and yields as it goes, so there is no
// one moment it finishes at. The walk is traced as a whole and the iterator
// handed on untouched: a span per page would need the iterator rebuilt around
// the tracer, and what a caller wants to know is how long the walk took.
func (c tracedCache) Scan(
	ctx context.Context, opts ...cache.ScanOption,
) iter.Seq2[string, error] {
	ctx, span := telemetry.CurrentTracer().Start(ctx, "celerity.cache.scan", c.attrs("")...)
	inner := c.inner.Scan(ctx, opts...)

	return func(yield func(string, error) bool) {
		defer span.End()
		var walked int
		for key, err := range inner {
			if err != nil {
				span.RecordError(err)
			}
			walked++
			if !yield(key, err) {
				// Stopped early, which is the ordinary way a walk ends: the
				// count is what was read rather than what there was.
				span.SetAttributes(
					telemetry.Int("cache.key_count", walked),
					telemetry.Bool("cache.stopped_early", true),
				)
				return
			}
		}
		span.SetAttributes(telemetry.Int("cache.key_count", walked))
	}
}

// TTL answers three values, which [telemetry.Traced] cannot carry, so the
// middle one is kept aside.
func (c tracedCache) TTL(
	ctx context.Context, key string,
) (time.Duration, bool, error) {
	var expires bool
	ttl, err := telemetry.Traced(ctx, "celerity.cache.ttl", c.attrs(key),
		func(ctx context.Context, span telemetry.Span) (time.Duration, error) {
			left, hasExpiry, err := c.inner.TTL(ctx, key)
			expires = hasExpiry
			span.SetAttributes(telemetry.Bool("cache.expires", hasExpiry))
			return left, err
		})
	return ttl, expires, err
}

func (c tracedCache) Append(ctx context.Context, key string, value string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.append", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.Append(ctx, key, value)
		})
}

func (c tracedCache) Connection(ctx context.Context) (cache.Connection, error) {
	return telemetry.Traced(ctx, "celerity.cache.connection", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) (cache.Connection, error) {
			return c.inner.Connection(ctx)
		})
}

func (c tracedCache) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.decrement", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.Decrement(ctx, key, delta)
		})
}

func (c tracedCache) Delete(ctx context.Context, key string) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.delete", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.Delete(ctx, key)
		})
}

func (c tracedCache) DeleteMany(ctx context.Context, keys []string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.delete_many", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.DeleteMany(ctx, keys)
		})
}

func (c tracedCache) Exists(ctx context.Context, key string) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.exists", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.Exists(ctx, key)
		})
}

func (c tracedCache) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.expire", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.Expire(ctx, key, ttl)
		})
}

func (c tracedCache) Get(ctx context.Context, key string) (string, error) {
	return telemetry.Traced(ctx, "celerity.cache.get", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (string, error) {
			return c.inner.Get(ctx, key)
		})
}

func (c tracedCache) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.get_many", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) (map[string]string, error) {
			return c.inner.GetMany(ctx, keys)
		})
}

func (c tracedCache) GetSet(ctx context.Context, key string, value string) (string, error) {
	return telemetry.Traced(ctx, "celerity.cache.get_set", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (string, error) {
			return c.inner.GetSet(ctx, key, value)
		})
}

func (c tracedCache) HashDelete(ctx context.Context, key string, fields []string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_delete", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.HashDelete(ctx, key, fields)
		})
}

func (c tracedCache) HashExists(ctx context.Context, key string, field string) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_exists", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.HashExists(ctx, key, field)
		})
}

func (c tracedCache) HashGet(ctx context.Context, key string, field string) (string, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_get", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (string, error) {
			return c.inner.HashGet(ctx, key, field)
		})
}

func (c tracedCache) HashGetAll(ctx context.Context, key string) (map[string]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_get_all", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (map[string]string, error) {
			return c.inner.HashGetAll(ctx, key)
		})
}

func (c tracedCache) HashIncrement(ctx context.Context, key string, field string, delta int64) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_increment", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.HashIncrement(ctx, key, field, delta)
		})
}

func (c tracedCache) HashKeys(ctx context.Context, key string) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_keys", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.HashKeys(ctx, key)
		})
}

func (c tracedCache) HashLen(ctx context.Context, key string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.hash_len", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.HashLen(ctx, key)
		})
}

func (c tracedCache) HashSet(ctx context.Context, key string, fields map[string]string) error {
	return telemetry.TracedCall(ctx, "celerity.cache.hash_set", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) error {
			return c.inner.HashSet(ctx, key, fields)
		})
}

func (c tracedCache) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.increment", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.Increment(ctx, key, delta)
		})
}

func (c tracedCache) IncrementFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return telemetry.Traced(ctx, "celerity.cache.increment_float", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (float64, error) {
			return c.inner.IncrementFloat(ctx, key, delta)
		})
}

func (c tracedCache) ListIndex(ctx context.Context, key string, index int64) (string, error) {
	return telemetry.Traced(ctx, "celerity.cache.list_index", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (string, error) {
			return c.inner.ListIndex(ctx, key, index)
		})
}

func (c tracedCache) ListLen(ctx context.Context, key string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.list_len", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.ListLen(ctx, key)
		})
}

func (c tracedCache) ListPop(ctx context.Context, key string, count int64, opts ...cache.EndOption) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.list_pop", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.ListPop(ctx, key, count, opts...)
		})
}

func (c tracedCache) ListPush(ctx context.Context, key string, values []string, opts ...cache.EndOption) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.list_push", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.ListPush(ctx, key, values, opts...)
		})
}

func (c tracedCache) ListRange(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.list_range", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.ListRange(ctx, key, start, stop)
		})
}

func (c tracedCache) ListTrim(ctx context.Context, key string, start int64, stop int64) error {
	return telemetry.TracedCall(ctx, "celerity.cache.list_trim", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) error {
			return c.inner.ListTrim(ctx, key, start, stop)
		})
}

func (c tracedCache) Persist(ctx context.Context, key string) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.persist", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.Persist(ctx, key)
		})
}

func (c tracedCache) Rename(ctx context.Context, key string, newKey string) error {
	return telemetry.TracedCall(ctx, "celerity.cache.rename", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) error {
			return c.inner.Rename(ctx, key, newKey)
		})
}

func (c tracedCache) Set(ctx context.Context, key string, value string, opts ...cache.SetOption) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.set", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.Set(ctx, key, value, opts...)
		})
}

func (c tracedCache) SetAdd(ctx context.Context, key string, members []string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_add", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SetAdd(ctx, key, members)
		})
}

func (c tracedCache) SetDiff(ctx context.Context, keys []string) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_diff", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.SetDiff(ctx, keys)
		})
}

func (c tracedCache) SetIntersect(ctx context.Context, keys []string) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_intersect", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.SetIntersect(ctx, keys)
		})
}

func (c tracedCache) SetIsMember(ctx context.Context, key string, member string) (bool, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_is_member", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (bool, error) {
			return c.inner.SetIsMember(ctx, key, member)
		})
}

func (c tracedCache) SetLen(ctx context.Context, key string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_len", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SetLen(ctx, key)
		})
}

func (c tracedCache) SetMany(ctx context.Context, values map[string]string) error {
	return telemetry.TracedCall(ctx, "celerity.cache.set_many", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) error {
			return c.inner.SetMany(ctx, values)
		})
}

func (c tracedCache) SetMembers(ctx context.Context, key string) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_members", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.SetMembers(ctx, key)
		})
}

func (c tracedCache) SetRemove(ctx context.Context, key string, members []string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_remove", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SetRemove(ctx, key, members)
		})
}

func (c tracedCache) SetUnion(ctx context.Context, keys []string) ([]string, error) {
	return telemetry.Traced(ctx, "celerity.cache.set_union", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) ([]string, error) {
			return c.inner.SetUnion(ctx, keys)
		})
}

func (c tracedCache) SortedSetAdd(ctx context.Context, key string, members []cache.SortedSetMember) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_add", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetAdd(ctx, key, members)
		})
}

func (c tracedCache) SortedSetCountByScore(ctx context.Context, key string, scores cache.ScoreRange) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_count_by_score", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetCountByScore(ctx, key, scores)
		})
}

func (c tracedCache) SortedSetIncrement(ctx context.Context, key string, member string, delta float64) (float64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_increment", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (float64, error) {
			return c.inner.SortedSetIncrement(ctx, key, member, delta)
		})
}

func (c tracedCache) SortedSetLen(ctx context.Context, key string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_len", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetLen(ctx, key)
		})
}

func (c tracedCache) SortedSetRange(ctx context.Context, key string, start int64, stop int64, opts ...cache.RangeOption) ([]cache.SortedSetMember, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_range", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) ([]cache.SortedSetMember, error) {
			return c.inner.SortedSetRange(ctx, key, start, stop, opts...)
		})
}

func (c tracedCache) SortedSetRangeByScore(ctx context.Context, key string, scores cache.ScoreRange, opts ...cache.RangeOption) ([]cache.SortedSetMember, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_range_by_score", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) ([]cache.SortedSetMember, error) {
			return c.inner.SortedSetRangeByScore(ctx, key, scores, opts...)
		})
}

func (c tracedCache) SortedSetRank(ctx context.Context, key string, member string, opts ...cache.RangeOption) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_rank", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetRank(ctx, key, member, opts...)
		})
}

func (c tracedCache) SortedSetRemove(ctx context.Context, key string, members []string) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_remove", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetRemove(ctx, key, members)
		})
}

func (c tracedCache) SortedSetRemoveByRank(ctx context.Context, key string, start int64, stop int64) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_remove_by_rank", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetRemoveByRank(ctx, key, start, stop)
		})
}

func (c tracedCache) SortedSetRemoveByScore(ctx context.Context, key string, scores cache.ScoreRange) (int64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_remove_by_score", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (int64, error) {
			return c.inner.SortedSetRemoveByScore(ctx, key, scores)
		})
}

func (c tracedCache) SortedSetScore(ctx context.Context, key string, member string) (float64, error) {
	return telemetry.Traced(ctx, "celerity.cache.sorted_set_score", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (float64, error) {
			return c.inner.SortedSetScore(ctx, key, member)
		})
}

func (c tracedCache) Transaction(ctx context.Context, queue func(cache.Tx)) ([]any, error) {
	return telemetry.Traced(ctx, "celerity.cache.transaction", c.attrs(""),
		func(ctx context.Context, _ telemetry.Span) ([]any, error) {
			return c.inner.Transaction(ctx, queue)
		})
}

func (c tracedCache) Type(ctx context.Context, key string) (cache.KeyType, error) {
	return telemetry.Traced(ctx, "celerity.cache.type", c.attrs(key),
		func(ctx context.Context, _ telemetry.Span) (cache.KeyType, error) {
			return c.inner.Type(ctx, key)
		})
}
