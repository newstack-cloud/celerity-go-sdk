package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// Transaction applies a group of writes as one.
//
// Queued twice rather than once: the function is run first against a recorder
// that only collects the keys, so that keys on different shards can be refused
// before anything is sent, and then again against the real pipeline. Running it
// once and checking as commands were queued would have the first command
// already sent by the time the second was found to be on another shard.
//
// The function is expected to do nothing but queue, which is what makes running
// it twice safe. Nothing in [cache.Tx] returns a value, so there is nothing for
// it to branch on.
func (c *redisCache) Transaction(
	ctx context.Context, queue func(cache.Tx),
) ([]any, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	if c.clustered {
		recorder := &recordingTx{cache: c}
		queue(recorder)
		if err := c.sameSlot("a transaction", recorder.keys); err != nil {
			return nil, err
		}
	}

	var queued int
	commands, err := client.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		tx := &pipelineTx{cache: c, pipe: pipe, ctx: ctx}
		queue(tx)
		queued = tx.queued
		return nil
	})

	// A command the cache refused comes back with the results rather than
	// instead of them, so the error is only fatal where nothing came back.
	if err != nil && !errors.Is(err, goredis.Nil) {
		return nil, c.failed(err, "applying a transaction", "")
	}

	if queued == 0 {
		return []any{}, nil
	}

	return answers(commands), nil
}

// answers is what each queued command replied, in the order they were queued.
//
// A command that the cache refused contributes its error rather than a value,
// so that a caller reading the results learns which one it was: the group was
// applied, and one member of it did nothing.
func answers(commands []goredis.Cmder) []any {
	results := make([]any, len(commands))
	for i, command := range commands {
		if err := command.Err(); err != nil && !errors.Is(err, goredis.Nil) {
			results[i] = err
			continue
		}
		results[i] = valueOf(command)
	}

	return results
}

// valueOf reads a command's reply through the interfaces the client's command
// types offer, which is the only way to get at it without knowing which
// command it was.
func valueOf(command goredis.Cmder) any {
	switch typed := command.(type) {
	case *goredis.StatusCmd:
		value, err := typed.Result()
		if errors.Is(err, goredis.Nil) {
			// A conditional write that did not hold, which is an answer.
			return false
		}
		return value
	case *goredis.StringCmd:
		value, err := typed.Result()
		if errors.Is(err, goredis.Nil) {
			return nil
		}
		return value
	case *goredis.IntCmd:
		value, _ := typed.Result()
		return value
	case *goredis.FloatCmd:
		value, _ := typed.Result()
		return value
	case *goredis.BoolCmd:
		value, _ := typed.Result()
		return value
	default:
		return nil
	}
}

// recordingTx collects the keys a transaction would touch, without queueing
// anything, so that a cluster's one-shard rule can be checked first.
type recordingTx struct {
	cache *redisCache
	keys  []string
}

func (t *recordingTx) with(keys ...string) cache.Tx {
	t.keys = append(t.keys, keys...)
	return t
}

func (t *recordingTx) Set(key, _ string, _ ...cache.SetOption) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Delete(key string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) GetSet(key, _ string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Append(key, _ string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Increment(key string, _ int64) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Decrement(key string, _ int64) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) IncrementFloat(key string, _ float64) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) HashSet(key string, _ map[string]string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) HashDelete(key string, _ []string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) HashIncrement(key, _ string, _ int64) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) ListPush(key string, _ []string, _ ...cache.EndOption) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) ListTrim(key string, _, _ int64) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) SetAdd(key string, _ []string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) SetRemove(key string, _ []string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) SortedSetAdd(key string, _ []cache.SortedSetMember) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) SortedSetRemove(key string, _ []string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) SortedSetIncrement(key, _ string, _ float64) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Expire(key string, _ time.Duration) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Persist(key string) cache.Tx {
	return t.with(t.cache.key(key))
}

func (t *recordingTx) Rename(key, newKey string) cache.Tx {
	return t.with(t.cache.key(key), t.cache.key(newKey))
}

// pipelineTx queues the writes of a transaction onto the real pipeline.
//
// Every key goes through the cache's own prefix, the same way a single command
// does, so a transaction on a session's cache writes where the rest of that
// session's writes go.
type pipelineTx struct {
	cache  *redisCache
	pipe   goredis.Pipeliner
	ctx    context.Context
	queued int
}

func (t *pipelineTx) done() cache.Tx {
	t.queued++
	return t
}

func (t *pipelineTx) Set(key, value string, opts ...cache.SetOption) cache.Tx {
	resolved := cache.ResolveSetOptions(opts)
	args := goredis.SetArgs{TTL: resolved.TTL}
	switch {
	case resolved.OnlyIfAbsent:
		args.Mode = "NX"
	case resolved.OnlyIfPresent:
		args.Mode = "XX"
	}
	t.pipe.SetArgs(t.ctx, t.cache.key(key), value, args)
	return t.done()
}

func (t *pipelineTx) Delete(key string) cache.Tx {
	t.pipe.Del(t.ctx, t.cache.key(key))
	return t.done()
}

func (t *pipelineTx) GetSet(key, value string) cache.Tx {
	t.pipe.GetSet(t.ctx, t.cache.key(key), value)
	return t.done()
}

func (t *pipelineTx) Append(key, value string) cache.Tx {
	t.pipe.Append(t.ctx, t.cache.key(key), value)
	return t.done()
}

func (t *pipelineTx) Increment(key string, delta int64) cache.Tx {
	t.pipe.IncrBy(t.ctx, t.cache.key(key), delta)
	return t.done()
}

func (t *pipelineTx) Decrement(key string, delta int64) cache.Tx {
	t.pipe.DecrBy(t.ctx, t.cache.key(key), delta)
	return t.done()
}

func (t *pipelineTx) IncrementFloat(key string, delta float64) cache.Tx {
	t.pipe.IncrByFloat(t.ctx, t.cache.key(key), delta)
	return t.done()
}

func (t *pipelineTx) HashSet(key string, fields map[string]string) cache.Tx {
	pairs := make([]any, 0, len(fields)*2)
	for field, value := range fields {
		pairs = append(pairs, field, value)
	}
	t.pipe.HSet(t.ctx, t.cache.key(key), pairs...)
	return t.done()
}

func (t *pipelineTx) HashDelete(key string, fields []string) cache.Tx {
	t.pipe.HDel(t.ctx, t.cache.key(key), fields...)
	return t.done()
}

func (t *pipelineTx) HashIncrement(key, field string, delta int64) cache.Tx {
	t.pipe.HIncrBy(t.ctx, t.cache.key(key), field, delta)
	return t.done()
}

func (t *pipelineTx) ListPush(
	key string, values []string, opts ...cache.EndOption,
) cache.Tx {
	push := t.pipe.RPush
	if cache.ResolveEndOptions(opts, cache.Right) == cache.Left {
		push = t.pipe.LPush
	}
	push(t.ctx, t.cache.key(key), asAny(values)...)
	return t.done()
}

func (t *pipelineTx) ListTrim(key string, start, stop int64) cache.Tx {
	t.pipe.LTrim(t.ctx, t.cache.key(key), start, stop)
	return t.done()
}

func (t *pipelineTx) SetAdd(key string, members []string) cache.Tx {
	t.pipe.SAdd(t.ctx, t.cache.key(key), asAny(members)...)
	return t.done()
}

func (t *pipelineTx) SetRemove(key string, members []string) cache.Tx {
	t.pipe.SRem(t.ctx, t.cache.key(key), asAny(members)...)
	return t.done()
}

func (t *pipelineTx) SortedSetAdd(key string, members []cache.SortedSetMember) cache.Tx {
	scored := make([]goredis.Z, len(members))
	for i, member := range members {
		scored[i] = goredis.Z{Member: member.Member, Score: member.Score}
	}
	t.pipe.ZAdd(t.ctx, t.cache.key(key), scored...)
	return t.done()
}

func (t *pipelineTx) SortedSetRemove(key string, members []string) cache.Tx {
	t.pipe.ZRem(t.ctx, t.cache.key(key), asAny(members)...)
	return t.done()
}

func (t *pipelineTx) SortedSetIncrement(key, member string, delta float64) cache.Tx {
	t.pipe.ZIncrBy(t.ctx, t.cache.key(key), delta, member)
	return t.done()
}

func (t *pipelineTx) Expire(key string, ttl time.Duration) cache.Tx {
	t.pipe.Expire(t.ctx, t.cache.key(key), ttl)
	return t.done()
}

func (t *pipelineTx) Persist(key string) cache.Tx {
	t.pipe.Persist(t.ctx, t.cache.key(key))
	return t.done()
}

func (t *pipelineTx) Rename(key, newKey string) cache.Tx {
	t.pipe.Rename(t.ctx, t.cache.key(key), t.cache.key(newKey))
	return t.done()
}
