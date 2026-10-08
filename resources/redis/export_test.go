package redis

import (
	"context"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
)

// Options is what a cache made of the values its deployment recorded, which is
// everything decided before a connection is attempted.
func Options(
	ctx context.Context, creds Credentials, ref resources.Ref,
) (addrs []string, clustered, encrypted bool, user, password, prefix string, err error) {
	options, prefix, err := (&redisCache{ref: ref, creds: creds}).options(ctx)
	if err != nil {
		return nil, false, false, "", "", "", err
	}

	return options.Addrs, options.IsClusterMode, options.TLSConfig != nil,
		options.Username, options.Password, prefix, nil
}

// SignsItsOwnPassword reports whether a cache was set up to mint a password per
// connection rather than to send one recorded value.
func SignsItsOwnPassword(
	ctx context.Context, creds Credentials, ref resources.Ref,
) (bool, error) {
	options, _, err := (&redisCache{ref: ref, creds: creds}).options(ctx)
	if err != nil {
		return false, err
	}

	return options.CredentialsProviderContext != nil, nil
}

// SlotOf is the shard a key belongs to, by the cluster protocol's own
// arithmetic. Exported to the test package because a wrong answer here is not
// something a running cache would reveal, it would answer every command
// correctly, for the wrong keys.
func SlotOf(key string) uint16 {
	return slotOf(key)
}

// HashTag is the part of a key the shard is computed from.
func HashTag(key string) string {
	return hashTag(key)
}

// GroupBySlot is how a batch is split into one command per shard.
func GroupBySlot(clustered bool, keys []string) [][]string {
	return (&redisCache{clustered: clustered}).bySlot(keys)
}
