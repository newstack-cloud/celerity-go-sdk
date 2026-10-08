package redis

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

func (c *redisCache) Exists(ctx context.Context, key string) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	count, err := client.Exists(ctx, c.key(key)).Result()
	if err != nil {
		return false, c.failed(err, "looking for", key)
	}
	return count > 0, nil
}

// TTL reports how long a key has left, and whether it has a lifetime at all.
//
// The protocol answers all three cases with one number, using -1 for a key
// that never expires and -2 for one that is not there. Both come back from the
// client as a negative duration, and a duration a caller has to compare against
// -2 is a trap, so they are separated here.
func (c *redisCache) TTL(ctx context.Context, key string) (time.Duration, bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, false, err
	}

	// Read as the integer the protocol answers with rather than as a duration.
	// TTL answers either a number of seconds or one of two codes, -2 for a key
	// that is not there and -1 for one with no expiry, and a code is not a
	// length of time: -2 as a duration is minus two nanoseconds, which is a
	// value the protocol never means. Reading the integer also makes this
	// independent of how the client chooses to represent the codes, which has
	// changed between its major versions.
	seconds, err := client.Do(ctx, "ttl", c.key(key)).Int64()
	if err != nil {
		return 0, false, c.failed(err, "reading the lifetime of", key)
	}

	switch seconds {
	case keyMissing:
		return 0, false, fmt.Errorf(
			"celerity: %s holds nothing under %q: %w",
			c.ref, key, cache.ErrNotFound,
		)
	case noExpiry:
		return 0, false, nil
	default:
		return time.Duration(seconds) * time.Second, true, nil
	}
}

// What TTL answers instead of a number of seconds.
const (
	// keyMissing is answered for a key that is not there at all.
	keyMissing = -2
	// noExpiry is answered for a key that is there and set to outlive the
	// process rather than to expire.
	noExpiry = -1
)

func (c *redisCache) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	set, err := client.Expire(ctx, c.key(key), ttl).Result()
	if err != nil {
		return false, c.failed(err, "setting the lifetime of", key)
	}
	return set, nil
}

func (c *redisCache) Persist(ctx context.Context, key string) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	removed, err := client.Persist(ctx, c.key(key)).Result()
	if err != nil {
		return false, c.failed(err, "removing the lifetime of", key)
	}
	return removed, nil
}

// Type reports which structure a key holds.
//
// The protocol answers "none" for a key that is not there rather than failing,
// which is the one answer the contract reports as not-found.
func (c *redisCache) Type(ctx context.Context, key string) (cache.KeyType, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return "", err
	}

	kind, err := client.Type(ctx, c.key(key)).Result()
	if err != nil {
		return "", c.failed(err, "reading the type of", key)
	}
	if kind == "none" {
		return "", fmt.Errorf("celerity: %s holds nothing under %q: %w",
			c.ref, key, cache.ErrNotFound)
	}
	return cache.KeyType(kind), nil
}

func (c *redisCache) Rename(ctx context.Context, key, newKey string) error {
	client, err := c.connect(ctx)
	if err != nil {
		return err
	}

	from, to := c.key(key), c.key(newKey)
	if err := c.sameSlot("renaming", []string{from, to}); err != nil {
		return err
	}

	err = client.Rename(ctx, from, to).Err()
	if err == nil {
		return nil
	}

	// The protocol fails a rename of a key that is not there, rather than
	// answering nil the way a read does, so the not-found case has to be read
	// out of the message.
	if isNoSuchKey(err) {
		return fmt.Errorf("celerity: %s holds nothing under %q: %w",
			c.ref, key, cache.ErrNotFound)
	}

	return c.failed(err, "renaming", key)
}

func isNoSuchKey(err error) bool {
	var redisErr goredis.Error
	return errors.As(err, &redisErr) && redisErr.Error() == "ERR no such key"
}

// Scan walks the cache's keys.
//
// One round trip per yield at most, so a handler that stops early has not paid
// for the rest. On a cluster every shard is walked in turn rather than in
// parallel, due to the nature of using an iterator, the reads need to be sequential.
func (c *redisCache) Scan(ctx context.Context, opts ...cache.ScanOption) iter.Seq2[string, error] {
	resolved := cache.ResolveScanOptions(opts)

	return func(yield func(string, error) bool) {
		client, err := c.connect(ctx)
		if err != nil {
			yield("", err)
			return
		}

		// The prefix is part of the pattern rather than applied afterwards, so
		// that a session sharing a cache does not walk another application's
		// keys at all, rather than walking them and discarding them.
		match := c.keyPrefix + resolved.Match
		if resolved.Match == "" {
			match = c.keyPrefix + "*"
		}

		for _, shard := range c.shards(ctx, client) {
			if !c.walk(ctx, shard, match, resolved, yield) {
				return
			}
		}
	}
}

// shards is the clients a walk has to visit: every master of a cluster, or the
// one client of a single instance.
//
// A scan is the only operation that has to be aimed at a particular node. Every
// other command is routed by its key, and a scan has no key.
func (c *redisCache) shards(ctx context.Context, client goredis.UniversalClient) []goredis.UniversalClient {
	cluster, ok := client.(*goredis.ClusterClient)
	if !ok {
		return []goredis.UniversalClient{client}
	}

	var masters []goredis.UniversalClient
	// The error is the one from the callback, and the callback returns none.
	_ = cluster.ForEachMaster(ctx, func(_ context.Context, node *goredis.Client) error {
		masters = append(masters, node)
		return nil
	})
	if len(masters) == 0 {
		return []goredis.UniversalClient{client}
	}
	return masters
}

// walk reads one shard through, reporting whether the caller wants more.
func (c *redisCache) walk(
	ctx context.Context,
	client goredis.UniversalClient,
	match string,
	opts cache.ScanOptions,
	yield func(string, error) bool,
) bool {
	var cursor uint64
	for {
		var (
			keys []string
			err  error
		)

		if opts.Type != "" {
			keys, cursor, err = client.ScanType(
				ctx, cursor, match, opts.Count, string(opts.Type)).Result()
		} else {
			keys, cursor, err = client.Scan(ctx, cursor, match, opts.Count).Result()
		}

		if err != nil {
			yield("", c.failed(err, "walking the keys", ""))
			return false
		}

		for _, key := range keys {
			if !yield(c.strip(key), nil) {
				return false
			}
		}

		// Zero is the cache saying it has been round the whole keyspace. A
		// round trip yielding nothing is ordinary before then, which is why
		// this is the cursor's answer and not an empty page.
		if cursor == 0 {
			return true
		}
	}
}
