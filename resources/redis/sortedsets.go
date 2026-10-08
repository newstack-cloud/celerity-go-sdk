package redis

import (
	"context"
	"strconv"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

func (c *redisCache) SortedSetAdd(
	ctx context.Context, key string, members []cache.SortedSetMember,
) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	scored := make([]goredis.Z, len(members))
	for i, member := range members {
		scored[i] = goredis.Z{Member: member.Member, Score: member.Score}
	}

	added, err := client.ZAdd(ctx, c.key(key), scored...).Result()
	if err != nil {
		return 0, c.failed(err, "adding to", key)
	}

	return added, nil
}

func (c *redisCache) SortedSetRemove(
	ctx context.Context, key string, members []string,
) (int64, error) {
	if len(members) == 0 {
		return 0, nil
	}
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	removed, err := client.ZRem(ctx, c.key(key), asAny(members)...).Result()
	if err != nil {
		return 0, c.failed(err, "removing from", key)
	}

	return removed, nil
}

func (c *redisCache) SortedSetScore(
	ctx context.Context, key, member string,
) (float64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	score, err := client.ZScore(ctx, c.key(key), member).Result()
	if err != nil {
		return 0, c.missing(err, "reading the score of a member of", key)
	}

	return score, nil
}

func (c *redisCache) SortedSetRank(
	ctx context.Context, key, member string, opts ...cache.RangeOption,
) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	rank := client.ZRank
	if cache.ResolveRangeOptions(opts).Descending {
		rank = client.ZRevRank
	}

	position, err := rank(ctx, c.key(key), member).Result()
	if err != nil {
		return 0, c.missing(err, "reading the rank of a member of", key)
	}

	return position, nil
}

// SortedSetRange reads members by position, always with their scores.
func (c *redisCache) SortedSetRange(
	ctx context.Context, key string, start, stop int64, opts ...cache.RangeOption,
) ([]cache.SortedSetMember, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	read := client.ZRangeWithScores
	if cache.ResolveRangeOptions(opts).Descending {
		read = client.ZRevRangeWithScores
	}

	scored, err := read(ctx, c.key(key), start, stop).Result()
	if err != nil {
		return nil, c.failed(err, "reading a range of", key)
	}
	return members(scored), nil
}

func (c *redisCache) SortedSetRangeByScore(
	ctx context.Context, key string, scores cache.ScoreRange, opts ...cache.RangeOption,
) ([]cache.SortedSetMember, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}

	resolved := cache.ResolveRangeOptions(opts)
	by := &goredis.ZRangeBy{
		Min:    bound(scores.Min, "-inf"),
		Max:    bound(scores.Max, "+inf"),
		Offset: resolved.Offset,
		Count:  resolved.Limit,
	}

	read := client.ZRangeByScoreWithScores
	if resolved.Descending {
		read = client.ZRevRangeByScoreWithScores
	}

	scored, err := read(ctx, c.key(key), by).Result()
	if err != nil {
		return nil, c.failed(err, "reading a score range of", key)
	}
	return members(scored), nil
}

func (c *redisCache) SortedSetIncrement(
	ctx context.Context, key, member string, delta float64,
) (float64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	score, err := client.ZIncrBy(ctx, c.key(key), delta, member).Result()
	if err != nil {
		return 0, c.failed(err, "incrementing a member of", key)
	}
	return score, nil
}

func (c *redisCache) SortedSetLen(ctx context.Context, key string) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	count, err := client.ZCard(ctx, c.key(key)).Result()
	if err != nil {
		return 0, c.failed(err, "counting", key)
	}
	return count, nil
}

func (c *redisCache) SortedSetCountByScore(
	ctx context.Context, key string, scores cache.ScoreRange,
) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	count, err := client.ZCount(
		ctx, c.key(key), bound(scores.Min, "-inf"), bound(scores.Max, "+inf")).Result()
	if err != nil {
		return 0, c.failed(err, "counting a score range of", key)
	}
	return count, nil
}

func (c *redisCache) SortedSetRemoveByRank(
	ctx context.Context, key string, start, stop int64,
) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	removed, err := client.ZRemRangeByRank(ctx, c.key(key), start, stop).Result()
	if err != nil {
		return 0, c.failed(err, "removing a rank range of", key)
	}
	return removed, nil
}

// SortedSetRemoveByScore removes the members within a score band.
//
// This is what trims a window, a rate limiter keeps timestamps as scores and removes
// everything older than the window on each pass, so the set stays the size of
// the window rather than the size of the traffic.
func (c *redisCache) SortedSetRemoveByScore(
	ctx context.Context, key string, scores cache.ScoreRange,
) (int64, error) {
	client, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}

	removed, err := client.ZRemRangeByScore(
		ctx, c.key(key), bound(scores.Min, "-inf"), bound(scores.Max, "+inf")).Result()
	if err != nil {
		return 0, c.failed(err, "removing a score range of", key)
	}
	return removed, nil
}

// bound is a score range's edge as the protocol takes it, which is a string so
// that it can be an infinity rather than a number.
func bound(score *float64, open string) string {
	if score == nil {
		return open
	}

	return strconv.FormatFloat(*score, 'f', -1, 64)
}

func members(scored []goredis.Z) []cache.SortedSetMember {
	out := make([]cache.SortedSetMember, len(scored))
	for i, z := range scored {
		out[i] = cache.SortedSetMember{Member: z.Member.(string), Score: z.Score}
	}
	return out
}
