package redis

import "context"

// Increment adds to a counter and returns what it became.
func (c *redisCache) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	value, err := client.IncrBy(ctx, c.key(key), delta).Result()
	if err != nil {
		return 0, c.failed(err, "incrementing", key)
	}
	return value, nil
}

func (c *redisCache) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	value, err := client.DecrBy(ctx, c.key(key), delta).Result()
	if err != nil {
		return 0, c.failed(err, "decrementing", key)
	}
	return value, nil
}

func (c *redisCache) IncrementFloat(
	ctx context.Context, key string, delta float64,
) (float64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	value, err := client.IncrByFloat(ctx, c.key(key), delta).Result()
	if err != nil {
		return 0, c.failed(err, "incrementing", key)
	}
	return value, nil
}
