package redis_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	redis "github.com/newstack-cloud/celerity-go-sdk/resources/redis"
)

// The shard a key belongs to is worked out here rather than asked of the cache,
// because it is needed before a command is sent. That makes it the one piece of
// this package a running cache cannot check for us: a wrong answer would group
// a batch by the wrong shard and the cache would answer every command
// correctly, for the wrong keys.
type SlotTestSuite struct {
	suite.Suite
}

func TestSlotTestSuite(t *testing.T) {
	suite.Run(t, new(SlotTestSuite))
}

func (s *SlotTestSuite) Test_a_key_lands_on_the_shard_the_protocol_says_it_does() {
	// The values the cluster specification's own examples give, which is what
	// makes this a check of the arithmetic rather than of itself.
	cases := []struct {
		key  string
		slot uint16
	}{
		{"foo", 12182},
		{"bar", 5061},
		{"hello", 866},
		{"", 0},
	}

	for _, test := range cases {
		s.Equal(test.slot, redis.SlotOf(test.key), "the slot of %q", test.key)
	}
}

func (s *SlotTestSuite) Test_keys_sharing_a_hash_tag_land_on_one_shard() {
	// Which is what makes a multi-key operation possible on a cluster at all,
	// the application decides what is co-located, by naming it.
	s.Equal(
		redis.SlotOf("{user:123}:name"),
		redis.SlotOf("{user:123}:prefs"),
	)
	s.Equal(redis.SlotOf("user:123"), redis.SlotOf("{user:123}:name"),
		"the tag hashes as though it were the whole key")
}

func (s *SlotTestSuite) Test_a_tag_that_is_not_one_leaves_the_whole_key_hashing() {
	cases := []string{
		"{unclosed:name",
		"{}:name",
		"no-braces",
	}

	for _, key := range cases {
		s.Equal(key, redis.HashTag(key),
			"%q carries no usable tag, so the whole key hashes", key)
	}
}

func (s *SlotTestSuite) Test_a_tag_is_the_first_pair_of_braces_only() {
	s.Equal("a", redis.HashTag("{a}{b}:name"))
	s.Equal("a", redis.HashTag("prefix{a}suffix"))
}

func (s *SlotTestSuite) Test_a_batch_on_one_instance_stays_one_command() {
	// Nothing to group: every key is reachable by one command, and splitting
	// would turn one round trip into several for no reason.
	grouped := redis.GroupBySlot(false, []string{"a", "b", "c"})

	s.Equal([][]string{{"a", "b", "c"}}, grouped)
}

func (s *SlotTestSuite) Test_a_batch_on_a_cluster_is_grouped_by_shard() {
	grouped := redis.GroupBySlot(true, []string{
		"{one}:a", "{two}:a", "{one}:b", "{two}:b",
	})

	s.Len(grouped, 2, "two tags, two shards")
	s.Equal([]string{"{one}:a", "{one}:b"}, grouped[0],
		"the order within a group is the order the keys were given in")
	s.Equal([]string{"{two}:a", "{two}:b"}, grouped[1])
}
