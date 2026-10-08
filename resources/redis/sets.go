package redis

import "context"

func (c *redisCache) SetAdd(
	ctx context.Context, key string, members []string,
) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	added, err := client.SAdd(ctx, c.key(key), asAny(members)...).Result()
	if err != nil {
		return 0, c.failed(err, "adding to", key)
	}
	return added, nil
}

func (c *redisCache) SetRemove(
	ctx context.Context, key string, members []string,
) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	removed, err := client.SRem(ctx, c.key(key), asAny(members)...).Result()
	if err != nil {
		return 0, c.failed(err, "removing from", key)
	}
	return removed, nil
}

func (c *redisCache) SetMembers(ctx context.Context, key string) ([]string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	members, err := client.SMembers(ctx, c.key(key)).Result()
	if err != nil {
		return nil, c.failed(err, "reading", key)
	}
	return members, nil
}

func (c *redisCache) SetIsMember(ctx context.Context, key, member string) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	present, err := client.SIsMember(ctx, c.key(key), member).Result()
	if err != nil {
		return false, c.failed(err, "looking in", key)
	}
	return present, nil
}

func (c *redisCache) SetLen(ctx context.Context, key string) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	count, err := client.SCard(ctx, c.key(key)).Result()
	if err != nil {
		return 0, c.failed(err, "counting", key)
	}
	return count, nil
}

// SetUnion, SetIntersect and SetDiff are computed by the cache across several
// keys, so on a cluster the keys have to be on one shard. There is no command
// that spans them, and a fan-out would have to read every set whole and combine
// them here, which for the sets these are worth using on is a different
// operation wearing the same name.

func (c *redisCache) SetUnion(ctx context.Context, keys []string) ([]string, error) {
	return c.combine(ctx, "taking the union of", keys, func(
		client redisCommands, names []string,
	) ([]string, error) {
		return client.SUnion(ctx, names...).Result()
	})
}

func (c *redisCache) SetIntersect(ctx context.Context, keys []string) ([]string, error) {
	return c.combine(ctx, "taking the intersection of", keys, func(
		client redisCommands, names []string,
	) ([]string, error) {
		return client.SInter(ctx, names...).Result()
	})
}

func (c *redisCache) SetDiff(ctx context.Context, keys []string) ([]string, error) {
	return c.combine(ctx, "taking the difference of", keys, func(
		client redisCommands, names []string,
	) ([]string, error) {
		return client.SDiff(ctx, names...).Result()
	})
}

// combine is the shape the three cross-key set operations share: refuse keys on
// different shards, then send the one command.
func (c *redisCache) combine(
	ctx context.Context,
	doing string,
	keys []string,
	run func(redisCommands, []string) ([]string, error),
) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	names := c.names(keys)
	if err := c.sameSlot(doing, names); err != nil {
		return nil, err
	}

	members, err := run(client, names)
	if err != nil {
		return nil, c.failed(err, doing, "")
	}
	return members, nil
}

func asAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
