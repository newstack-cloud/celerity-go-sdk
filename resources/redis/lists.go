package redis

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// ListPush adds to one end of a list, the tail unless asked otherwise.
//
// The tail for a push and the head for a pop are the two defaults that make an
// untouched list a queue, which is what a list is most often wanted for.
func (c *redisCache) ListPush(
	ctx context.Context, key string, values []string, opts ...cache.EndOption,
) (int64, error) {
	if len(values) == 0 {
		return c.ListLen(ctx, key)
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	items := make([]any, len(values))
	for i, value := range values {
		items[i] = value
	}

	push := client.RPush
	if cache.ResolveEndOptions(opts, cache.Right) == cache.Left {
		push = client.LPush
	}

	length, err := push(ctx, c.key(key), items...).Result()
	if err != nil {
		return 0, c.failed(err, "pushing onto", key)
	}
	return length, nil
}

// ListPop takes from one end of a list, the head unless asked otherwise.
//
// An empty slice for a list that holds nothing, rather than not-found: a pop
// that found nothing is the ordinary answer for a queue that is empty, and a
// handler draining one should not have to tell that from a failure.
func (c *redisCache) ListPop(
	ctx context.Context, key string, count int64, opts ...cache.EndOption,
) ([]string, error) {
	if count <= 0 {
		count = 1
	}
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	pop := client.LPopCount
	if cache.ResolveEndOptions(opts, cache.Left) == cache.Right {
		pop = client.RPopCount
	}

	values, err := pop(ctx, c.key(key), int(count)).Result()
	if err != nil {
		if isNil(err) {
			return []string{}, nil
		}
		return nil, c.failed(err, "popping from", key)
	}
	return values, nil
}

func (c *redisCache) ListRange(
	ctx context.Context, key string, start, stop int64,
) ([]string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	values, err := client.LRange(ctx, c.key(key), start, stop).Result()
	if err != nil {
		return nil, c.failed(err, "reading a range of", key)
	}
	return values, nil
}

func (c *redisCache) ListLen(ctx context.Context, key string) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	length, err := client.LLen(ctx, c.key(key)).Result()
	if err != nil {
		return 0, c.failed(err, "measuring", key)
	}
	return length, nil
}

// ListTrim discards everything outside a range.
func (c *redisCache) ListTrim(ctx context.Context, key string, start, stop int64) error {
	client, err := c.connect(ctx)
	if err != nil {
		return err
	}

	if err := client.LTrim(ctx, c.key(key), start, stop).Err(); err != nil {
		return c.failed(err, "trimming", key)
	}
	return nil
}

func (c *redisCache) ListIndex(ctx context.Context, key string, index int64) (string, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return "", err
	}

	value, err := client.LIndex(ctx, c.key(key), index).Result()
	if err != nil {
		return "", c.missing(err, "reading an element of", key)
	}
	return value, nil
}
