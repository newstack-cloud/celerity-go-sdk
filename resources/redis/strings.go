package redis

import (
	"context"
	"errors"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

func (c *redisCache) Get(ctx context.Context, key string) (string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return "", err
	}

	value, err := client.Get(ctx, c.key(key)).Result()
	if err != nil {
		return "", c.missing(err, "reading", key)
	}
	return value, nil
}

// Set stores a value, for a time and on a condition.
//
// One command whichever options were given, through SetArgs, rather than the
// three the protocol has for the combinations of them. Without it a write with
// both a lifetime and a condition would have to be two commands, and two
// commands is not what the caller asked for.
func (c *redisCache) Set(
	ctx context.Context, key, value string, opts ...cache.SetOption,
) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	resolved := cache.ResolveSetOptions(opts)
	args := goredis.SetArgs{TTL: resolved.TTL}
	switch {
	case resolved.OnlyIfAbsent:
		args.Mode = "NX"
	case resolved.OnlyIfPresent:
		args.Mode = "XX"
	}

	err = client.SetArgs(ctx, c.key(key), value, args).Err()
	// A condition that did not hold answers the same way a missing key does,
	// and here it is the answer rather than a failure: the caller asked to be
	// told whether it won the race it entered.
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}

	if err != nil {
		return false, c.failed(err, "writing", key)
	}

	return true, nil
}

func (c *redisCache) Delete(ctx context.Context, key string) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	removed, err := client.Del(ctx, c.key(key)).Result()
	if err != nil {
		return false, c.failed(err, "deleting", key)
	}
	return removed > 0, nil
}

func (c *redisCache) GetSet(ctx context.Context, key, value string) (string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return "", err
	}

	previous, err := client.GetSet(ctx, c.key(key), value).Result()
	if err != nil {
		return "", c.missing(err, "replacing", key)
	}
	return previous, nil
}

func (c *redisCache) Append(ctx context.Context, key, value string) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	length, err := client.Append(ctx, c.key(key), value).Result()
	if err != nil {
		return 0, c.failed(err, "appending to", key)
	}
	return length, nil
}

// GetMany reads many keys, one command per shard.
//
// Absent keys are left out rather than given an empty string, since an empty
// string is a value a cache can hold and the two would be indistinguishable.
func (c *redisCache) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) == 0 {
		return map[string]string{}, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	found := make(map[string]string, len(keys))
	for _, group := range c.bySlot(c.names(keys)) {
		values, err := client.MGet(ctx, group...).Result()
		if err != nil {
			return nil, c.failed(err, "reading a batch", "")
		}

		for i, value := range values {
			// Nil is the cache saying it holds nothing under that key, which is
			// the one answer this does not report.
			if text, ok := value.(string); ok {
				found[c.strip(group[i])] = text
			}
		}
	}

	return found, nil
}

// SetMany stores many values, one command per shard.
//
// Each shard's command is atomic and the shards are not, which is the
// behaviour on every cache that shards: there is no command that spans them.
func (c *redisCache) SetMany(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return err
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, c.key(key))
	}

	for _, group := range c.bySlot(keys) {
		pairs := make([]any, 0, len(group)*2)
		for _, key := range group {
			pairs = append(pairs, key, values[c.strip(key)])
		}
		if err := client.MSet(ctx, pairs...).Err(); err != nil {
			return c.failed(err, "writing a batch", "")
		}
	}

	return nil
}

func (c *redisCache) DeleteMany(ctx context.Context, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	var removed int64
	for _, group := range c.bySlot(c.names(keys)) {
		count, err := client.Del(ctx, group...).Result()
		if err != nil {
			return removed, c.failed(err, "deleting a batch", "")
		}
		removed += count
	}

	return removed, nil
}
