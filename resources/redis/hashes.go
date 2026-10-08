package redis

import (
	"context"
	"fmt"
)

func (c *redisCache) HashGet(ctx context.Context, key, field string) (string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return "", err
	}

	value, err := client.HGet(ctx, c.key(key), field).Result()
	if err != nil {
		return "", c.missing(err, "reading a field of", key)
	}
	return value, nil
}

func (c *redisCache) HashSet(ctx context.Context, key string, fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return err
	}

	pairs := make([]any, 0, len(fields)*2)
	for field, value := range fields {
		pairs = append(pairs, field, value)
	}
	if err := client.HSet(ctx, c.key(key), pairs...).Err(); err != nil {
		return c.failed(err, "writing fields of", key)
	}
	return nil
}

func (c *redisCache) HashDelete(
	ctx context.Context, key string, fields []string,
) (int64, error) {
	if len(fields) == 0 {
		return 0, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	removed, err := client.HDel(ctx, c.key(key), fields...).Result()
	if err != nil {
		return 0, c.failed(err, "deleting fields of", key)
	}
	return removed, nil
}

// HashGetAll reads every field.
//
// An empty map for a key that holds nothing rather than not-found, a hash with
// no fields does not exist on this protocol, so the two cases are the same
// answer and a map can carry it.
func (c *redisCache) HashGetAll(ctx context.Context, key string) (map[string]string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	fields, err := client.HGetAll(ctx, c.key(key)).Result()
	if err != nil {
		return nil, c.failed(err, "reading", key)
	}
	return fields, nil
}

func (c *redisCache) HashExists(ctx context.Context, key, field string) (bool, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return false, err
	}

	present, err := client.HExists(ctx, c.key(key), field).Result()
	if err != nil {
		return false, c.failed(err, "looking for a field of", key)
	}
	return present, nil
}

func (c *redisCache) HashIncrement(
	ctx context.Context, key, field string, delta int64,
) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	value, err := client.HIncrBy(ctx, c.key(key), field, delta).Result()
	if err != nil {
		return 0, c.failed(err, fmt.Sprintf("incrementing %q of", field), key)
	}
	return value, nil
}

func (c *redisCache) HashKeys(ctx context.Context, key string) ([]string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	fields, err := client.HKeys(ctx, c.key(key)).Result()
	if err != nil {
		return nil, c.failed(err, "reading the fields of", key)
	}
	return fields, nil
}

func (c *redisCache) HashLen(ctx context.Context, key string) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	count, err := client.HLen(ctx, c.key(key)).Result()
	if err != nil {
		return 0, c.failed(err, "counting the fields of", key)
	}
	return count, nil
}
