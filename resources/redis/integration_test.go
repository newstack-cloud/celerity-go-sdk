//go:build integration

// The cache against a real Valkey instance, which is what every managed cache speaks and
// what a development session runs.
//
// What a unit test mock cannot establish: that a value round-trips, that the server
// rather than this package does the arithmetic, that the five structures behave
// as the contract says, that a transaction is applied as one, and that a key
// prefix keeps two applications on one cache apart.
//
// Run with: bash scripts/run-tests.sh --with-integration
package redis_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
	"github.com/newstack-cloud/celerity-go-sdk/resources/redis"
)

type RedisIntegrationTestSuite struct {
	suite.Suite
}

func TestRedisIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(RedisIntegrationTestSuite))
}

// -- Strings ---------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_value_is_written_read_and_deleted() {
	ctx := context.Background()
	sessions := s.cacheOn("strings:")

	stored, err := sessions.Set(ctx, "orders:1", `{"id":1}`, cache.Expires(time.Minute))
	s.Require().NoError(err)
	s.True(stored)

	value, err := sessions.Get(ctx, "orders:1")
	s.Require().NoError(err)
	s.Equal(`{"id":1}`, value)

	removed, err := sessions.Delete(ctx, "orders:1")
	s.Require().NoError(err)
	s.True(removed)

	_, err = sessions.Get(ctx, "orders:1")
	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_key_that_was_never_written_is_not_found() {
	_, err := s.cacheOn("strings:").Get(context.Background(), "never-written")

	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_deleting_nothing_reports_that_nothing_was_there() {
	removed, err := s.cacheOn("strings:").Delete(context.Background(), "never-written")

	s.Require().NoError(err)
	s.False(removed, "deleting nothing is an answer rather than a failure")
}

func (s *RedisIntegrationTestSuite) Test_a_write_that_must_not_overwrite() {
	// Exactly one of several handlers racing to claim a key gets true, which is
	// what makes this a lock rather than a write.
	ctx := context.Background()
	sessions := s.cacheOn("strings:")
	_, err := sessions.Delete(ctx, "claim")
	s.Require().NoError(err)

	first, err := sessions.Set(ctx, "claim", "mine", cache.IfNotExists())
	s.Require().NoError(err)
	s.True(first)

	second, err := sessions.Set(ctx, "claim", "yours", cache.IfNotExists())
	s.Require().NoError(err)
	s.False(second, "the second write lost the race and says so")

	value, err := sessions.Get(ctx, "claim")
	s.Require().NoError(err)
	s.Equal("mine", value)
}

func (s *RedisIntegrationTestSuite) Test_a_write_that_must_not_create() {
	ctx := context.Background()
	sessions := s.cacheOn("strings:")
	_, err := sessions.Delete(ctx, "refresh-only")
	s.Require().NoError(err)

	created, err := sessions.Set(ctx, "refresh-only", "x", cache.IfExists())
	s.Require().NoError(err)
	s.False(created)

	_, err = sessions.Get(ctx, "refresh-only")
	s.Require().ErrorIs(err, cache.ErrNotFound, "nothing should have been created")
}

func (s *RedisIntegrationTestSuite) Test_a_conditional_write_still_takes_a_lifetime() {
	// Both in one command, which is the reason this package builds the write
	// through SetArgs rather than picking one of the protocol's three forms.
	ctx := context.Background()
	sessions := s.cacheOn("strings:")
	_, err := sessions.Delete(ctx, "claim-with-ttl")
	s.Require().NoError(err)

	stored, err := sessions.Set(ctx, "claim-with-ttl", "mine",
		cache.IfNotExists(), cache.Expires(time.Minute))
	s.Require().NoError(err)
	s.Require().True(stored)

	ttl, expires, err := sessions.TTL(ctx, "claim-with-ttl")
	s.Require().NoError(err)
	s.True(expires)
	s.Positive(ttl)
}

func (s *RedisIntegrationTestSuite) Test_a_value_is_taken_and_replaced_in_one_step() {
	ctx := context.Background()
	sessions := s.cacheOn("strings:")
	_, err := sessions.Set(ctx, "handover", "first")
	s.Require().NoError(err)

	previous, err := sessions.GetSet(ctx, "handover", "second")
	s.Require().NoError(err)
	s.Equal("first", previous)

	value, err := sessions.Get(ctx, "handover")
	s.Require().NoError(err)
	s.Equal("second", value)
}

func (s *RedisIntegrationTestSuite) Test_replacing_a_value_that_was_not_there() {
	ctx := context.Background()
	sessions := s.cacheOn("strings:")
	_, err := sessions.Delete(ctx, "nothing-to-hand-over")
	s.Require().NoError(err)

	_, err = sessions.GetSet(ctx, "nothing-to-hand-over", "new")

	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_value_is_appended_to() {
	ctx := context.Background()
	sessions := s.cacheOn("strings:")
	_, err := sessions.Delete(ctx, "log")
	s.Require().NoError(err)

	length, err := sessions.Append(ctx, "log", "one")
	s.Require().NoError(err)
	s.EqualValues(3, length, "appending to nothing creates the key")

	length, err = sessions.Append(ctx, "log", "-two")
	s.Require().NoError(err)
	s.EqualValues(7, length)

	value, err := sessions.Get(ctx, "log")
	s.Require().NoError(err)
	s.Equal("one-two", value)
}

// -- Batches ---------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_many_values_are_written_read_and_deleted() {
	ctx := context.Background()
	sessions := s.cacheOn("batch:")

	s.Require().NoError(sessions.SetMany(ctx, map[string]string{
		"a": "1", "b": "2", "c": "3",
	}))

	found, err := sessions.GetMany(ctx, []string{"a", "b", "c"})
	s.Require().NoError(err)
	s.Equal(map[string]string{"a": "1", "b": "2", "c": "3"}, found)

	removed, err := sessions.DeleteMany(ctx, []string{"a", "b", "c", "never-written"})
	s.Require().NoError(err)
	s.EqualValues(3, removed, "only the keys that were there are counted")
}

func (s *RedisIntegrationTestSuite) Test_a_batch_read_leaves_out_what_is_not_there() {
	// The absence is the absence of a map entry rather than an empty string,
	// because an empty string is a value a cache can hold.
	ctx := context.Background()
	sessions := s.cacheOn("batch:")
	s.Require().NoError(sessions.SetMany(ctx, map[string]string{"present": ""}))

	found, err := sessions.GetMany(ctx, []string{"present", "absent"})

	s.Require().NoError(err)
	s.Len(found, 1)
	value, ok := found["present"]
	s.True(ok)
	s.Equal("", value, "a key holding an empty string is present and empty")
}

func (s *RedisIntegrationTestSuite) Test_an_empty_batch_asks_the_cache_nothing() {
	ctx := context.Background()
	sessions := s.cacheOn("batch:")

	found, err := sessions.GetMany(ctx, nil)
	s.Require().NoError(err)
	s.Empty(found)

	s.Require().NoError(sessions.SetMany(ctx, nil))

	removed, err := sessions.DeleteMany(ctx, nil)
	s.Require().NoError(err)
	s.Zero(removed)
}

// -- Keys ------------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_key_with_no_lifetime_says_so() {
	// Three answers where the protocol has one number, which is the reason
	// this is a duration and a bool rather than a duration to compare with -2.
	ctx := context.Background()
	sessions := s.cacheOn("keys:")
	_, err := sessions.Set(ctx, "forever", "x")
	s.Require().NoError(err)

	ttl, expires, err := sessions.TTL(ctx, "forever")

	s.Require().NoError(err)
	s.False(expires)
	s.Zero(ttl)
}

func (s *RedisIntegrationTestSuite) Test_the_lifetime_of_a_key_that_is_not_there() {
	_, _, err := s.cacheOn("keys:").TTL(context.Background(), "never-written")

	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_lifetime_is_given_and_taken_away() {
	ctx := context.Background()
	sessions := s.cacheOn("keys:")
	_, err := sessions.Set(ctx, "temporary", "x")
	s.Require().NoError(err)

	set, err := sessions.Expire(ctx, "temporary", time.Hour)
	s.Require().NoError(err)
	s.True(set)

	_, expires, err := sessions.TTL(ctx, "temporary")
	s.Require().NoError(err)
	s.True(expires)

	removed, err := sessions.Persist(ctx, "temporary")
	s.Require().NoError(err)
	s.True(removed)

	_, expires, err = sessions.TTL(ctx, "temporary")
	s.Require().NoError(err)
	s.False(expires)
}

func (s *RedisIntegrationTestSuite) Test_a_lifetime_on_a_key_that_is_not_there() {
	set, err := s.cacheOn("keys:").Expire(context.Background(), "never-written", time.Hour)

	s.Require().NoError(err)
	s.False(set)
}

func (s *RedisIntegrationTestSuite) Test_a_key_reports_which_structure_it_holds() {
	ctx := context.Background()
	sessions := s.cacheOn("keys:")

	_, err := sessions.Set(ctx, "a-string", "x")
	s.Require().NoError(err)
	_, err = sessions.ListPush(ctx, "a-list", []string{"x"})
	s.Require().NoError(err)
	_, err = sessions.SetAdd(ctx, "a-set", []string{"x"})
	s.Require().NoError(err)
	_, err = sessions.SortedSetAdd(ctx, "a-sorted-set",
		[]cache.SortedSetMember{{Member: "x", Score: 1}})
	s.Require().NoError(err)
	s.Require().NoError(sessions.HashSet(ctx, "a-hash", map[string]string{"f": "x"}))

	for key, kind := range map[string]cache.KeyType{
		"a-string":     cache.KeyString,
		"a-list":       cache.KeyList,
		"a-set":        cache.KeySet,
		"a-sorted-set": cache.KeySortedSet,
		"a-hash":       cache.KeyHash,
	} {
		actual, err := sessions.Type(ctx, key)
		s.Require().NoError(err, key)
		s.Equal(kind, actual, key)
	}
}

func (s *RedisIntegrationTestSuite) Test_the_type_of_a_key_that_is_not_there() {
	// The protocol answers "none" rather than failing, and the contract calls
	// that not-found.
	_, err := s.cacheOn("keys:").Type(context.Background(), "never-written")

	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_key_is_renamed() {
	ctx := context.Background()
	sessions := s.cacheOn("keys:")
	_, err := sessions.Set(ctx, "old-name", "value")
	s.Require().NoError(err)
	_, err = sessions.Delete(ctx, "new-name")
	s.Require().NoError(err)

	s.Require().NoError(sessions.Rename(ctx, "old-name", "new-name"))

	value, err := sessions.Get(ctx, "new-name")
	s.Require().NoError(err)
	s.Equal("value", value)
	_, err = sessions.Get(ctx, "old-name")
	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_renaming_a_key_that_is_not_there() {
	// The protocol fails this rather than answering nil, so the not-found case
	// has to be recognised from the failure.
	err := s.cacheOn("keys:").Rename(context.Background(), "never-written", "somewhere")

	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_key_is_looked_for() {
	ctx := context.Background()
	sessions := s.cacheOn("keys:")
	_, err := sessions.Set(ctx, "here", "x")
	s.Require().NoError(err)

	present, err := sessions.Exists(ctx, "here")
	s.Require().NoError(err)
	s.True(present)

	present, err = sessions.Exists(ctx, "never-written")
	s.Require().NoError(err)
	s.False(present)
}

// -- Scan ------------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_the_keys_of_a_cache_are_walked() {
	ctx := context.Background()
	sessions := s.cacheOn("scan:")
	s.Require().NoError(sessions.SetMany(ctx, map[string]string{
		"session:1": "a", "session:2": "b", "order:1": "c",
	}))

	var walked []string
	for key, err := range sessions.Scan(ctx, cache.Matching("session:*")) {
		s.Require().NoError(err)
		walked = append(walked, key)
	}

	s.ElementsMatch([]string{"session:1", "session:2"}, walked,
		"the pattern is applied by the cache, and the prefix comes back off")
}

func (s *RedisIntegrationTestSuite) Test_a_walk_stops_when_the_caller_does() {
	ctx := context.Background()
	sessions := s.cacheOn("scan-stop:")
	// Enough keys that a walk of five at a time takes several round trips,
	// which is what there is to stop part way through. Nothing reads them.
	values := map[string]string{}
	for i := range 50 {
		values[fmt.Sprintf("key:%02d", i)] = "x"
	}
	s.Require().NoError(sessions.SetMany(ctx, values))

	var seen int
	for _, err := range sessions.Scan(ctx, cache.PerRoundTrip(5)) {
		s.Require().NoError(err)
		seen++
		break
	}

	s.Equal(1, seen, "stopping the range stops the walk")
}

func (s *RedisIntegrationTestSuite) Test_a_walk_keeps_only_one_structure() {
	ctx := context.Background()
	sessions := s.cacheOn("scan-type:")
	_, err := sessions.Set(ctx, "a-string", "x")
	s.Require().NoError(err)
	_, err = sessions.ListPush(ctx, "a-list", []string{"x"})
	s.Require().NoError(err)

	var walked []string
	for key, err := range sessions.Scan(ctx, cache.Holding(cache.KeyList)) {
		s.Require().NoError(err)
		walked = append(walked, key)
	}

	s.Equal([]string{"a-list"}, walked)
}

// -- Counters --------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_counter_is_incremented_by_the_server() {
	ctx := context.Background()
	sessions := s.cacheOn("counters:")
	_, err := sessions.Delete(ctx, "orders")
	s.Require().NoError(err)

	first, err := sessions.Increment(ctx, "orders", 1)
	s.Require().NoError(err)
	s.EqualValues(1, first, "a key that holds nothing counts as zero")

	second, err := sessions.Increment(ctx, "orders", 5)
	s.Require().NoError(err)
	s.EqualValues(6, second)

	third, err := sessions.Decrement(ctx, "orders", 2)
	s.Require().NoError(err)
	s.EqualValues(4, third)
}

func (s *RedisIntegrationTestSuite) Test_a_counter_that_is_not_whole() {
	ctx := context.Background()
	sessions := s.cacheOn("counters:")
	_, err := sessions.Delete(ctx, "rate")
	s.Require().NoError(err)

	value, err := sessions.IncrementFloat(ctx, "rate", 1.5)
	s.Require().NoError(err)
	s.InDelta(1.5, value, 0.0001)

	value, err = sessions.IncrementFloat(ctx, "rate", -0.25)
	s.Require().NoError(err)
	s.InDelta(1.25, value, 0.0001)
}

// -- Hashes ----------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_hash_is_written_and_read_a_field_at_a_time() {
	ctx := context.Background()
	sessions := s.cacheOn("hashes:")
	_, err := sessions.Delete(ctx, "user:1")
	s.Require().NoError(err)

	s.Require().NoError(sessions.HashSet(ctx, "user:1", map[string]string{
		"name": "Ada", "city": "London",
	}))

	name, err := sessions.HashGet(ctx, "user:1", "name")
	s.Require().NoError(err)
	s.Equal("Ada", name)

	// A second write leaves the fields it does not name alone, which is the
	// point of a hash over a serialised string.
	s.Require().NoError(sessions.HashSet(ctx, "user:1", map[string]string{"name": "Grace"}))
	all, err := sessions.HashGetAll(ctx, "user:1")
	s.Require().NoError(err)
	s.Equal(map[string]string{"name": "Grace", "city": "London"}, all)

	fields, err := sessions.HashKeys(ctx, "user:1")
	s.Require().NoError(err)
	s.ElementsMatch([]string{"name", "city"}, fields)

	count, err := sessions.HashLen(ctx, "user:1")
	s.Require().NoError(err)
	s.EqualValues(2, count)
}

func (s *RedisIntegrationTestSuite) Test_a_hash_field_is_looked_for_and_removed() {
	ctx := context.Background()
	sessions := s.cacheOn("hashes:")
	s.Require().NoError(sessions.HashSet(ctx, "user:2", map[string]string{"name": "Ada"}))

	present, err := sessions.HashExists(ctx, "user:2", "name")
	s.Require().NoError(err)
	s.True(present)

	removed, err := sessions.HashDelete(ctx, "user:2", []string{"name", "never-set"})
	s.Require().NoError(err)
	s.EqualValues(1, removed)

	_, err = sessions.HashGet(ctx, "user:2", "name")
	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_hash_that_is_not_there_reads_as_empty() {
	// An empty map rather than not-found: a hash with no fields does not exist
	// on this protocol, so the two cases are one answer.
	all, err := s.cacheOn("hashes:").HashGetAll(context.Background(), "never-written")

	s.Require().NoError(err)
	s.Empty(all)
}

func (s *RedisIntegrationTestSuite) Test_a_hash_field_is_a_counter() {
	ctx := context.Background()
	sessions := s.cacheOn("hashes:")
	_, err := sessions.Delete(ctx, "counts")
	s.Require().NoError(err)

	value, err := sessions.HashIncrement(ctx, "counts", "views", 3)
	s.Require().NoError(err)
	s.EqualValues(3, value)

	value, err = sessions.HashIncrement(ctx, "counts", "views", -1)
	s.Require().NoError(err)
	s.EqualValues(2, value)
}

// -- Lists -----------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_list_is_a_queue_by_default() {
	// Push to the tail, pop from the head, which is the pair of defaults that
	// makes an untouched list first-in-first-out.
	ctx := context.Background()
	sessions := s.cacheOn("lists:")
	_, err := sessions.Delete(ctx, "queue")
	s.Require().NoError(err)

	length, err := sessions.ListPush(ctx, "queue", []string{"first", "second"})
	s.Require().NoError(err)
	s.EqualValues(2, length)

	values, err := sessions.ListPop(ctx, "queue", 1)
	s.Require().NoError(err)
	s.Equal([]string{"first"}, values)
}

func (s *RedisIntegrationTestSuite) Test_a_list_is_a_stack_when_asked() {
	ctx := context.Background()
	sessions := s.cacheOn("lists:")
	_, err := sessions.Delete(ctx, "stack")
	s.Require().NoError(err)
	_, err = sessions.ListPush(ctx, "stack", []string{"first", "second"})
	s.Require().NoError(err)

	values, err := sessions.ListPop(ctx, "stack", 1, cache.AtRight())

	s.Require().NoError(err)
	s.Equal([]string{"second"}, values)
}

func (s *RedisIntegrationTestSuite) Test_popping_an_empty_list_is_an_answer() {
	values, err := s.cacheOn("lists:").ListPop(context.Background(), "never-written", 1)

	s.Require().NoError(err)
	s.Empty(values, "a drained queue is ordinary, not a failure")
}

func (s *RedisIntegrationTestSuite) Test_a_list_is_read_measured_and_bounded() {
	ctx := context.Background()
	sessions := s.cacheOn("lists:")
	_, err := sessions.Delete(ctx, "feed")
	s.Require().NoError(err)
	_, err = sessions.ListPush(ctx, "feed", []string{"a", "b", "c", "d"})
	s.Require().NoError(err)

	values, err := sessions.ListRange(ctx, "feed", 0, -1)
	s.Require().NoError(err)
	s.Equal([]string{"a", "b", "c", "d"}, values)

	length, err := sessions.ListLen(ctx, "feed")
	s.Require().NoError(err)
	s.EqualValues(4, length)

	element, err := sessions.ListIndex(ctx, "feed", 1)
	s.Require().NoError(err)
	s.Equal("b", element)

	// Push then trim is what bounds a list, so it never grows past what is
	// wanted however long the application runs.
	s.Require().NoError(sessions.ListTrim(ctx, "feed", 0, 1))
	values, err = sessions.ListRange(ctx, "feed", 0, -1)
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, values)
}

func (s *RedisIntegrationTestSuite) Test_an_element_outside_a_list() {
	ctx := context.Background()
	sessions := s.cacheOn("lists:")
	_, err := sessions.Delete(ctx, "short")
	s.Require().NoError(err)
	_, err = sessions.ListPush(ctx, "short", []string{"only"})
	s.Require().NoError(err)

	_, err = sessions.ListIndex(ctx, "short", 5)

	s.Require().ErrorIs(err, cache.ErrNotFound)
}

// -- Sets ------------------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_set_holds_each_member_once() {
	ctx := context.Background()
	sessions := s.cacheOn("sets:")
	_, err := sessions.Delete(ctx, "tags")
	s.Require().NoError(err)

	added, err := sessions.SetAdd(ctx, "tags", []string{"go", "redis", "go"})
	s.Require().NoError(err)
	s.EqualValues(2, added, "the repeat was not a new member")

	added, err = sessions.SetAdd(ctx, "tags", []string{"go"})
	s.Require().NoError(err)
	s.Zero(added)

	members, err := sessions.SetMembers(ctx, "tags")
	s.Require().NoError(err)
	s.ElementsMatch([]string{"go", "redis"}, members)

	count, err := sessions.SetLen(ctx, "tags")
	s.Require().NoError(err)
	s.EqualValues(2, count)
}

func (s *RedisIntegrationTestSuite) Test_membership_of_a_set() {
	ctx := context.Background()
	sessions := s.cacheOn("sets:")
	_, err := sessions.SetAdd(ctx, "seen", []string{"visitor:1"})
	s.Require().NoError(err)

	present, err := sessions.SetIsMember(ctx, "seen", "visitor:1")
	s.Require().NoError(err)
	s.True(present)

	present, err = sessions.SetIsMember(ctx, "seen", "visitor:2")
	s.Require().NoError(err)
	s.False(present)

	removed, err := sessions.SetRemove(ctx, "seen", []string{"visitor:1", "visitor:2"})
	s.Require().NoError(err)
	s.EqualValues(1, removed)
}

func (s *RedisIntegrationTestSuite) Test_sets_are_combined_by_the_cache() {
	ctx := context.Background()
	sessions := s.cacheOn("sets:")
	_, err := sessions.Delete(ctx, "left")
	s.Require().NoError(err)
	_, err = sessions.Delete(ctx, "right")
	s.Require().NoError(err)
	_, err = sessions.SetAdd(ctx, "left", []string{"a", "b"})
	s.Require().NoError(err)
	_, err = sessions.SetAdd(ctx, "right", []string{"b", "c"})
	s.Require().NoError(err)

	union, err := sessions.SetUnion(ctx, []string{"left", "right"})
	s.Require().NoError(err)
	s.ElementsMatch([]string{"a", "b", "c"}, union)

	intersection, err := sessions.SetIntersect(ctx, []string{"left", "right"})
	s.Require().NoError(err)
	s.Equal([]string{"b"}, intersection)

	difference, err := sessions.SetDiff(ctx, []string{"left", "right"})
	s.Require().NoError(err)
	s.Equal([]string{"a"}, difference)

	// None of them stored the result.
	_, err = sessions.Type(ctx, "left")
	s.Require().NoError(err)
}

// -- Sorted sets -----------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_sorted_set_is_kept_in_score_order() {
	ctx := context.Background()
	sessions := s.cacheOn("zsets:")
	_, err := sessions.Delete(ctx, "leaderboard")
	s.Require().NoError(err)

	added, err := sessions.SortedSetAdd(ctx, "leaderboard", []cache.SortedSetMember{
		{Member: "ada", Score: 10},
		{Member: "grace", Score: 30},
		{Member: "alan", Score: 20},
	})
	s.Require().NoError(err)
	s.EqualValues(3, added)

	lowest, err := sessions.SortedSetRange(ctx, "leaderboard", 0, -1)
	s.Require().NoError(err)
	s.Equal([]cache.SortedSetMember{
		{Member: "ada", Score: 10},
		{Member: "alan", Score: 20},
		{Member: "grace", Score: 30},
	}, lowest)

	// The top of a leaderboard without reading the whole of it.
	top, err := sessions.SortedSetRange(ctx, "leaderboard", 0, 1, cache.Descending())
	s.Require().NoError(err)
	s.Equal([]cache.SortedSetMember{
		{Member: "grace", Score: 30},
		{Member: "alan", Score: 20},
	}, top)
}

func (s *RedisIntegrationTestSuite) Test_a_members_score_and_rank() {
	ctx := context.Background()
	sessions := s.cacheOn("zsets:")
	_, err := sessions.Delete(ctx, "ranked")
	s.Require().NoError(err)
	_, err = sessions.SortedSetAdd(ctx, "ranked", []cache.SortedSetMember{
		{Member: "ada", Score: 10}, {Member: "grace", Score: 30},
	})
	s.Require().NoError(err)

	score, err := sessions.SortedSetScore(ctx, "ranked", "grace")
	s.Require().NoError(err)
	s.InDelta(30.0, score, 0.0001)

	rank, err := sessions.SortedSetRank(ctx, "ranked", "grace")
	s.Require().NoError(err)
	s.EqualValues(1, rank, "counting up from the lowest score")

	rank, err = sessions.SortedSetRank(ctx, "ranked", "grace", cache.Descending())
	s.Require().NoError(err)
	s.EqualValues(0, rank, "counting down from the highest")

	_, err = sessions.SortedSetScore(ctx, "ranked", "never-added")
	s.Require().ErrorIs(err, cache.ErrNotFound)

	_, err = sessions.SortedSetRank(ctx, "ranked", "never-added")
	s.Require().ErrorIs(err, cache.ErrNotFound)
}

func (s *RedisIntegrationTestSuite) Test_a_score_range_may_be_open_at_either_end() {
	ctx := context.Background()
	sessions := s.cacheOn("zsets:")
	_, err := sessions.Delete(ctx, "window")
	s.Require().NoError(err)
	_, err = sessions.SortedSetAdd(ctx, "window", []cache.SortedSetMember{
		{Member: "a", Score: 1}, {Member: "b", Score: 2}, {Member: "c", Score: 3},
	})
	s.Require().NoError(err)

	closed, err := sessions.SortedSetRangeByScore(ctx, "window", cache.Scores(2, 3))
	s.Require().NoError(err)
	s.Equal([]string{"b", "c"}, names(closed))

	from, err := sessions.SortedSetRangeByScore(ctx, "window", cache.ScoresFrom(2))
	s.Require().NoError(err)
	s.Equal([]string{"b", "c"}, names(from))

	upTo, err := sessions.SortedSetRangeByScore(ctx, "window", cache.ScoresUpTo(2))
	s.Require().NoError(err)
	s.Equal([]string{"a", "b"}, names(upTo))

	all, err := sessions.SortedSetRangeByScore(ctx, "window", cache.AllScores())
	s.Require().NoError(err)
	s.Equal([]string{"a", "b", "c"}, names(all))
}

func (s *RedisIntegrationTestSuite) Test_a_score_range_is_paged() {
	ctx := context.Background()
	sessions := s.cacheOn("zsets:")
	_, err := sessions.Delete(ctx, "paged")
	s.Require().NoError(err)
	_, err = sessions.SortedSetAdd(ctx, "paged", []cache.SortedSetMember{
		{Member: "a", Score: 1}, {Member: "b", Score: 2},
		{Member: "c", Score: 3}, {Member: "d", Score: 4},
	})
	s.Require().NoError(err)

	page, err := sessions.SortedSetRangeByScore(
		ctx, "paged", cache.AllScores(), cache.Page(1, 2))

	s.Require().NoError(err)
	s.Equal([]string{"b", "c"}, names(page))
}

func (s *RedisIntegrationTestSuite) Test_a_score_is_incremented_and_counted() {
	ctx := context.Background()
	sessions := s.cacheOn("zsets:")
	_, err := sessions.Delete(ctx, "scores")
	s.Require().NoError(err)

	score, err := sessions.SortedSetIncrement(ctx, "scores", "ada", 5)
	s.Require().NoError(err)
	s.InDelta(5.0, score, 0.0001, "a member that was not there is added")

	score, err = sessions.SortedSetIncrement(ctx, "scores", "ada", 2)
	s.Require().NoError(err)
	s.InDelta(7.0, score, 0.0001)

	count, err := sessions.SortedSetLen(ctx, "scores")
	s.Require().NoError(err)
	s.EqualValues(1, count)

	count, err = sessions.SortedSetCountByScore(ctx, "scores", cache.ScoresFrom(7))
	s.Require().NoError(err)
	s.EqualValues(1, count)

	count, err = sessions.SortedSetCountByScore(ctx, "scores", cache.ScoresUpTo(6))
	s.Require().NoError(err)
	s.Zero(count)
}

func (s *RedisIntegrationTestSuite) Test_a_sorted_set_is_trimmed_by_rank_and_by_score() {
	// What keeps a rate limiter's window the size of the window rather than
	// the size of the traffic.
	ctx := context.Background()
	sessions := s.cacheOn("zsets:")
	_, err := sessions.Delete(ctx, "trimmed")
	s.Require().NoError(err)
	_, err = sessions.SortedSetAdd(ctx, "trimmed", []cache.SortedSetMember{
		{Member: "a", Score: 1}, {Member: "b", Score: 2},
		{Member: "c", Score: 3}, {Member: "d", Score: 4},
	})
	s.Require().NoError(err)

	removed, err := sessions.SortedSetRemoveByScore(ctx, "trimmed", cache.ScoresUpTo(1))
	s.Require().NoError(err)
	s.EqualValues(1, removed)

	removed, err = sessions.SortedSetRemoveByRank(ctx, "trimmed", 0, 0)
	s.Require().NoError(err)
	s.EqualValues(1, removed)

	left, err := sessions.SortedSetRange(ctx, "trimmed", 0, -1)
	s.Require().NoError(err)
	s.Equal([]string{"c", "d"}, names(left))

	removed, err = sessions.SortedSetRemove(ctx, "trimmed", []string{"c", "never-added"})
	s.Require().NoError(err)
	s.EqualValues(1, removed)
}

// -- Transactions ----------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_a_group_of_writes_is_applied_as_one() {
	ctx := context.Background()
	sessions := s.cacheOn("tx:")
	_, err := sessions.Delete(ctx, "name")
	s.Require().NoError(err)
	_, err = sessions.Delete(ctx, "visits")
	s.Require().NoError(err)

	results, err := sessions.Transaction(ctx, func(tx cache.Tx) {
		tx.Set("name", "Ada").
			Increment("visits", 1).
			Expire("name", time.Hour)
	})

	s.Require().NoError(err)
	s.Require().Len(results, 3, "one answer per queued command, in order")
	s.Equal("OK", results[0])
	s.EqualValues(int64(1), results[1])
	s.Equal(true, results[2])

	value, err := sessions.Get(ctx, "name")
	s.Require().NoError(err)
	s.Equal("Ada", value)
}

func (s *RedisIntegrationTestSuite) Test_a_transaction_writes_where_the_prefix_says() {
	// The defect this is here for: a transaction that queued commands on a raw
	// pipeline would write unprefixed keys, and on a shared development cache
	// that is another application's data.
	ctx := context.Background()
	prefixed := s.cacheOn("tx-prefix:")
	unprefixed := s.cacheOn("")

	_, err := prefixed.Transaction(ctx, func(tx cache.Tx) {
		tx.Set("scoped", "value")
	})
	s.Require().NoError(err)

	value, err := unprefixed.Get(ctx, "tx-prefix:scoped")
	s.Require().NoError(err)
	s.Equal("value", value)

	_, err = unprefixed.Get(ctx, "scoped")
	s.Require().ErrorIs(err, cache.ErrNotFound, "nothing should have been written unprefixed")
}

func (s *RedisIntegrationTestSuite) Test_a_transaction_covers_every_structure() {
	ctx := context.Background()
	sessions := s.cacheOn("tx-all:")
	for _, key := range []string{"h", "l", "s", "z", "str"} {
		_, err := sessions.Delete(ctx, key)
		s.Require().NoError(err)
	}

	results, err := sessions.Transaction(ctx, func(tx cache.Tx) {
		tx.Set("str", "value").
			Append("str", "-more").
			HashSet("h", map[string]string{"f": "1"}).
			HashIncrement("h", "n", 2).
			ListPush("l", []string{"a", "b"}).
			ListTrim("l", 0, 0).
			SetAdd("s", []string{"x"}).
			SortedSetAdd("z", []cache.SortedSetMember{{Member: "m", Score: 1}}).
			SortedSetIncrement("z", "m", 2).
			Persist("str")
	})

	s.Require().NoError(err)
	s.Len(results, 10)

	value, err := sessions.Get(ctx, "str")
	s.Require().NoError(err)
	s.Equal("value-more", value)

	fields, err := sessions.HashGetAll(ctx, "h")
	s.Require().NoError(err)
	s.Equal(map[string]string{"f": "1", "n": "2"}, fields)

	list, err := sessions.ListRange(ctx, "l", 0, -1)
	s.Require().NoError(err)
	s.Equal([]string{"a"}, list)

	score, err := sessions.SortedSetScore(ctx, "z", "m")
	s.Require().NoError(err)
	s.InDelta(3.0, score, 0.0001)
}

func (s *RedisIntegrationTestSuite) Test_a_transaction_reports_a_write_it_could_not_make() {
	// The group is applied and one member of it does nothing, which is not the
	// same as the group failing, so the answer is in the results.
	ctx := context.Background()
	sessions := s.cacheOn("tx-refused:")
	_, err := sessions.Set(ctx, "taken", "first")
	s.Require().NoError(err)

	results, err := sessions.Transaction(ctx, func(tx cache.Tx) {
		tx.Set("taken", "second", cache.IfNotExists())
	})

	s.Require().NoError(err)
	s.Require().Len(results, 1)
	s.Equal(false, results[0], "the conditional write did not hold")

	value, err := sessions.Get(ctx, "taken")
	s.Require().NoError(err)
	s.Equal("first", value)
}

func (s *RedisIntegrationTestSuite) Test_a_transaction_that_queues_nothing() {
	results, err := s.cacheOn("tx:").Transaction(
		context.Background(), func(cache.Tx) {})

	s.Require().NoError(err)
	s.Empty(results)
}

// -- Key prefixes ----------------------------------------------------------

func (s *RedisIntegrationTestSuite) Test_two_applications_on_one_cache_are_kept_apart() {
	ctx := context.Background()
	orders := s.cacheOn("orders:")
	invoices := s.cacheOn("invoices:")

	_, err := orders.Set(ctx, "1", "an order", cache.Expires(time.Minute))
	s.Require().NoError(err)
	_, err = invoices.Set(ctx, "1", "an invoice", cache.Expires(time.Minute))
	s.Require().NoError(err)

	value, err := orders.Get(ctx, "1")
	s.Require().NoError(err)
	s.Equal("an order", value)

	unprefixed := s.cacheOn("")
	value, err = unprefixed.Get(ctx, "orders:1")
	s.Require().NoError(err)
	s.Equal("an order", value, "the prefix is part of the key the cache holds")
}

func (s *RedisIntegrationTestSuite) Test_a_value_stops_being_readable_once_it_expires() {
	ctx := context.Background()
	sessions := s.cacheOn("ttl:")

	_, err := sessions.Set(ctx, "orders", "x", cache.Expires(time.Second))
	s.Require().NoError(err)

	s.Require().Eventually(func() bool {
		_, err := sessions.Get(ctx, "orders")
		return errors.Is(err, cache.ErrNotFound)
	}, 5*time.Second, 200*time.Millisecond)
}

// names is the members of a sorted-set read, without the scores, for a case
// about which members came back rather than what they scored.
func names(members []cache.SortedSetMember) []string {
	out := make([]string, len(members))
	for i, member := range members {
		out[i] = member.Member
	}
	return out
}

// cacheOn builds a handle to the Valkey instance the suite runs against, described the
// way a development session's deployment describes one: no encryption and no
// auth token, because there is nothing there to protect.
func (s *RedisIntegrationTestSuite) cacheOn(prefix string) cache.Client {
	client, err := redis.New(refFor(resources.KindCache, "ordersCache",
		map[string]string{
			"ordersCache_host":      "127.0.0.1",
			"ordersCache_port":      portOr("CELERITY_TEST_VALKEY_PORT", "6379"),
			"ordersCache_tls":       "false",
			"ordersCache_keyPrefix": prefix,
		}), nil)
	s.Require().NoError(err)
	return client
}

func portOr(name, fallback string) string {
	if port := os.Getenv(name); port != "" {
		return port
	}
	return fallback
}
