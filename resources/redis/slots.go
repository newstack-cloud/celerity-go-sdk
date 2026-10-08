package redis

import (
	"fmt"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// How many slots the keyspace of a cluster is divided into, which
// the redis protocol defines as fixed.
const hashSlots = 16384

// The shard a key belongs to, by the cluster protocol's own
// arithmetic.
//
// Computed here rather than asked of the cache, because it is needed before a
// command is sent: to group a batch by shard, and to refuse an operation whose
// keys are on different ones. The client knows a key's slot too, but only as
// it routes a command it has already been given.
func slotOf(key string) uint16 {
	return crc16([]byte(hashTag(key))) % hashSlots
}

// The part of a key the slot is computed from.
//
// A key carrying braces hashes on what is between the first '{' and the next
// '}', which is what lets related keys be put on one shard deliberately:
// {user:123}:name and {user:123}:prefs hash the same. An empty or unclosed
// pair is not a tag, and the whole key is used.
func hashTag(key string) string {
	start := strings.Index(key, "{")
	if start < 0 {
		return key
	}

	end := strings.Index(key[start+1:], "}")
	if end <= 0 {
		return key
	}

	return key[start+1 : start+1+end]
}

// crc16 is CRC-16/XMODEM, which is the function the cluster specification names.
// see https://redis.io/docs/latest/operate/oss_and_stack/reference/cluster-spec/#appendix
//
// Written out rather than tabulated, a 256-entry table is the faster form and
// the one the specification's appendix prints, but a slot is computed a handful
// of times per command and the loop is the version that can be read against
// the polynomial.
func crc16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
				continue
			}
			crc <<= 1
		}
	}
	return crc
}

// sameSlot refuses keys that are not all on one shard.
//
// Only asked on a cluster, on a single instance every key is reachable by one
// command, so there is nothing to refuse. Reported before the command is sent,
// because the cache's own answer names a slot number rather than the keys that
// disagreed.
func (c *redisCache) sameSlot(doing string, keys []string) error {
	if !c.clustered || len(keys) <= 1 {
		return nil
	}

	first := slotOf(keys[0])
	for _, key := range keys[1:] {
		if slotOf(key) == first {
			continue
		}

		return fmt.Errorf(
			"celerity: %s in %s needs %q and %q on one shard, and they are on %d and %d: "+
				"put them in one with a hash tag, {tag}: %w",
			doing, c.ref, c.strip(keys[0]), c.strip(key), first, slotOf(key),
			cache.ErrCrossSlot,
		)
	}
	return nil
}

// bySlot groups keys by the shard they are on, keeping the order they were
// given in within each group.
//
// This is what makes a batch work on a cluster, one command per shard rather than one
// command the cache would refuse. On a single instance there is one group, so
// the batch is the one command it always was.
func (c *redisCache) bySlot(keys []string) [][]string {
	if !c.clustered || len(keys) <= 1 {
		return [][]string{keys}
	}

	order := make([]uint16, 0, len(keys))
	groups := make(map[uint16][]string, len(keys))
	for _, key := range keys {
		slot := slotOf(key)
		if _, seen := groups[slot]; !seen {
			order = append(order, slot)
		}
		groups[slot] = append(groups[slot], key)
	}

	grouped := make([][]string, 0, len(order))
	for _, slot := range order {
		grouped = append(grouped, groups[slot])
	}
	return grouped
}
