package celeritytest_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/celeritytest"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/sqldb"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// A double is only worth having if it answers the way the real resource does,
// so what these assert are the rules the contract states rather than the ones
// this implementation happens to have.
type DoublesTestSuite struct {
	suite.Suite
	ctx context.Context
}

func TestDoublesTestSuite(t *testing.T) {
	suite.Run(t, new(DoublesTestSuite))
}

func (s *DoublesTestSuite) SetupTest() { s.ctx = context.Background() }

type item struct {
	Status  string  `json:"status"`
	Version float64 `json:"version"`
	Total   float64 `json:"total"`
}

func (s *DoublesTestSuite) store() *celeritytest.Datastore {
	return celeritytest.NewDatastore("ordersTable")
}

func (s *DoublesTestSuite) Test_a_read_of_a_key_that_holds_nothing_is_an_absence() {
	var out item
	_, err := s.store().Get(s.ctx, datastore.Key{Partition: "nothing"}, &out)

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrNotFound)
}

func (s *DoublesTestSuite) Test_a_conditional_write_is_refused_when_the_condition_does_not_hold() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open", Version: 1}))

	_, err := store.Put(s.ctx, key, item{Status: "paid", Version: 2},
		datastore.If(datastore.Eq("version", 7)))

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed, "the item was not in the state described")

	var held item
	_, readErr := store.Get(s.ctx, key, &held)
	s.Require().NoError(readErr)
	s.Equal("open", held.Status, "and nothing was written")
}

func (s *DoublesTestSuite) Test_an_insert_that_must_not_overwrite_refuses_an_item_already_there() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}

	_, first := store.Put(s.ctx, key, item{Status: "open"},
		datastore.If(datastore.NotExists("status")))
	_, second := store.Put(s.ctx, key, item{Status: "paid"},
		datastore.If(datastore.NotExists("status")))

	s.Require().NoError(first, "nothing was there, so the write goes through")
	s.Require().Error(second)
	s.ErrorIs(second, datastore.ErrConditionFailed)
}

func (s *DoublesTestSuite) Test_a_revision_from_a_read_is_what_a_later_write_requires() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open"}))

	var held item
	revision, err := store.Get(s.ctx, key, &held)
	s.Require().NoError(err)

	_, mine := store.Put(s.ctx, key, item{Status: "paid"}, datastore.IfUnchanged(revision))
	s.Require().NoError(mine, "nothing else wrote in between")

	_, stale := store.Put(s.ctx, key, item{Status: "cancelled"}, datastore.IfUnchanged(revision))
	s.Require().Error(stale, "the revision names a version that is no longer current")
	s.ErrorIs(stale, datastore.ErrConditionFailed)
}

func (s *DoublesTestSuite) Test_a_revision_that_did_not_come_from_a_read_is_refused() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open"}))

	_, err := store.Put(s.ctx, key, item{Status: "paid"},
		datastore.IfUnchanged(datastore.Revision{}))

	s.ErrorIs(err, datastore.ErrInvalidRevision,
		"writing without the precondition asked for would be worse than failing")
}

func (s *DoublesTestSuite) Test_a_query_reads_one_partition_narrowed_by_the_sort_key() {
	store := s.store()
	for _, sortKey := range []string{"2026-01", "2026-02", "2026-03"} {
		s.Require().NoError(store.Seed(
			datastore.Key{Partition: "acme", Sort: sortKey}, item{Status: sortKey}))
	}
	s.Require().NoError(store.Seed(
		datastore.Key{Partition: "other", Sort: "2026-02"}, item{Status: "elsewhere"}))

	var found []item
	_, err := store.Query(s.ctx, datastore.Query{
		Partition: "acme",
		Sort:      datastore.SortBetween("2026-02", "2026-03"),
	}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 2, "one partition, and only the part of it asked for")
	s.Equal("2026-02", found[0].Status)
	s.Equal("2026-03", found[1].Status)
}

func (s *DoublesTestSuite) Test_a_query_reads_the_partition_backwards_when_asked() {
	store := s.store()
	for _, sortKey := range []string{"a", "b", "c"} {
		s.Require().NoError(store.Seed(
			datastore.Key{Partition: "acme", Sort: sortKey}, item{Status: sortKey}))
	}

	var found []item
	_, err := store.Query(s.ctx, datastore.Query{Partition: "acme", Descending: true}, &found)

	s.Require().NoError(err)
	s.Equal([]string{"c", "b", "a"}, []string{found[0].Status, found[1].Status, found[2].Status})
}

func (s *DoublesTestSuite) Test_a_query_pages_and_resumes_where_it_stopped() {
	store := s.store()
	for _, sortKey := range []string{"a", "b", "c"} {
		s.Require().NoError(store.Seed(
			datastore.Key{Partition: "acme", Sort: sortKey}, item{Status: sortKey}))
	}

	var first []item
	cursor, err := store.Query(s.ctx, datastore.Query{Partition: "acme", Limit: 2}, &first)
	s.Require().NoError(err)
	s.Require().Len(first, 2)
	s.Require().True(cursor.More(), "there is more to read")

	var second []item
	next, err := store.Query(s.ctx,
		datastore.Query{Partition: "acme", Limit: 2, Cursor: string(cursor)}, &second)
	s.Require().NoError(err)
	s.Require().Len(second, 1)
	s.False(next.More(), "and the end answers with nothing to resume from")
}

func (s *DoublesTestSuite) Test_a_filter_drops_items_after_they_are_read() {
	store := s.store()
	s.Require().NoError(store.Seed(datastore.Key{Partition: "acme", Sort: "1"},
		item{Status: "open", Total: 5}))
	s.Require().NoError(store.Seed(datastore.Key{Partition: "acme", Sort: "2"},
		item{Status: "paid", Total: 50}))

	var found []item
	filter := datastore.Gt("total", 10)
	_, err := store.Query(s.ctx, datastore.Query{Partition: "acme", Filter: &filter}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.Equal("paid", found[0].Status)
}

// cents is a named numeric type, which is the ordinary way an application
// spells money and the shape a type switch would not match.
type cents int

func (s *DoublesTestSuite) Test_a_filter_compares_a_named_numeric_type() {
	// The real store marshals by reflecting, so it accepts a condition on a
	// named type. The double has to as well, or a handler that works in a
	// deployment fails a test for a reason that is the double's.
	store := s.store()
	s.Require().NoError(store.Seed(datastore.Key{Partition: "acme", Sort: "1"},
		item{Status: "open", Total: 5}))
	s.Require().NoError(store.Seed(datastore.Key{Partition: "acme", Sort: "2"},
		item{Status: "paid", Total: 50}))

	var found []item
	filter := datastore.Gt("total", cents(10))
	_, err := store.Query(s.ctx, datastore.Query{Partition: "acme", Filter: &filter}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 1, "the named type compared as the number it is")
	s.Equal("paid", found[0].Status)
}

func (s *DoublesTestSuite) Test_an_update_changes_part_of_an_item_and_leaves_the_rest() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open", Total: 10, Version: 1}))

	_, err := store.Update(s.ctx, key, []datastore.Update{
		datastore.Set("status", "paid"),
		datastore.Increment("version", 1),
	})
	s.Require().NoError(err)

	var held item
	_, readErr := store.Get(s.ctx, key, &held)
	s.Require().NoError(readErr)
	s.Equal("paid", held.Status)
	s.Equal(float64(2), held.Version)
	s.Equal(float64(10), held.Total, "a field the update did not name is left alone")
}

func (s *DoublesTestSuite) Test_an_update_of_a_key_that_holds_nothing_reports_absence() {
	// Rather than creating it, so the same code does not make a half-populated
	// item on one store and report not-found on another.
	_, err := s.store().Update(s.ctx, datastore.Key{Partition: "nothing"},
		[]datastore.Update{datastore.Set("status", "paid")})

	s.ErrorIs(err, datastore.ErrNotFound)
}

func (s *DoublesTestSuite) Test_an_atomic_write_applies_every_operation_or_none() {
	store := s.store()
	first := datastore.Key{Partition: "acme", Sort: "1"}
	second := datastore.Key{Partition: "acme", Sort: "2"}
	s.Require().NoError(store.Seed(first, item{Status: "open"}))

	err := store.Atomically(s.ctx, "acme", []datastore.AtomicOp{
		datastore.AtomicPut(second, item{Status: "new"}),
		// Refused, so the write above has to be undone with it.
		datastore.AtomicPut(first, item{Status: "paid"},
			datastore.If(datastore.Eq("status", "cancelled"))),
	})

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed)
	s.Equal(1, store.Len(), "the operation that would have worked was undone with the one that did not")
}

func (s *DoublesTestSuite) Test_an_atomic_write_outside_its_partition_is_refused() {
	err := s.store().Atomically(s.ctx, "acme", []datastore.AtomicOp{
		datastore.AtomicPut(datastore.Key{Partition: "elsewhere"}, item{}),
	})

	s.ErrorIs(err, datastore.ErrWrongPartition)
}

func (s *DoublesTestSuite) Test_a_batch_get_reports_the_keys_that_held_nothing() {
	store := s.store()
	s.Require().NoError(store.Seed(datastore.Key{Partition: "a"}, item{Status: "here"}))

	var found []item
	missing, err := store.BatchGet(s.ctx, []datastore.Key{
		{Partition: "a"}, {Partition: "b"},
	}, &found)

	s.Require().NoError(err)
	s.Len(found, 1)
	s.Equal([]datastore.Key{{Partition: "b"}}, missing,
		"a key that holds nothing is absent rather than an error")
}

func (s *DoublesTestSuite) Test_a_scan_reads_every_partition() {
	store := s.store()
	s.Require().NoError(store.Seed(datastore.Key{Partition: "a"}, item{Status: "one"}))
	s.Require().NoError(store.Seed(datastore.Key{Partition: "b"}, item{Status: "two"}))

	var found []item
	cursor, err := store.Scan(s.ctx, datastore.Scan{}, &found)

	s.Require().NoError(err)
	s.Len(found, 2, "a scan reads the whole store rather than one partition")
	s.False(cursor.More())
}

func (s *DoublesTestSuite) Test_a_scan_narrows_with_a_filter_and_a_projection() {
	store := s.store()
	s.Require().NoError(store.Seed(datastore.Key{Partition: "a"},
		item{Status: "open", Total: 5}))
	s.Require().NoError(store.Seed(datastore.Key{Partition: "b"},
		item{Status: "paid", Total: 50}))

	var found []item
	filter := datastore.Eq("status", "paid")
	_, err := store.Scan(s.ctx, datastore.Scan{Filter: &filter, Project: []string{"status"}}, &found)

	s.Require().NoError(err)
	s.Require().Len(found, 1)
	s.Equal("paid", found[0].Status)
	s.Zero(found[0].Total, "a field the projection left out does not come back")
}

func (s *DoublesTestSuite) Test_a_delete_removes_an_item_and_deleting_nothing_is_not_a_failure() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open"}))

	s.Require().NoError(store.Delete(s.ctx, key))
	s.Equal(0, store.Len())

	s.NoError(store.Delete(s.ctx, key), "which is what makes a retry safe")
}

func (s *DoublesTestSuite) Test_a_conditional_delete_is_refused_when_the_condition_does_not_hold() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open"}))

	err := store.Delete(s.ctx, key, datastore.If(datastore.Eq("status", "paid")))

	s.ErrorIs(err, datastore.ErrConditionFailed)
	s.Equal(1, store.Len(), "and the item is still there")
}

func (s *DoublesTestSuite) Test_a_batch_write_is_not_atomic_and_reports_what_it_could_not_apply() {
	store := s.store()

	unapplied, err := store.BatchWrite(s.ctx, []datastore.BatchOp{
		datastore.PutOp(datastore.Key{Partition: "a"}, item{Status: "one"}),
		datastore.DeleteOp(datastore.Key{Partition: "never-there"}),
	})

	s.Require().NoError(err)
	s.Empty(unapplied, "a delete of nothing is not a failure")
	s.Equal(1, store.Len())
}

func (s *DoublesTestSuite) Test_conditions_compose_with_all_and_any() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, item{Status: "open", Total: 50}))

	_, both := store.Put(s.ctx, key, item{Status: "paid"}, datastore.If(datastore.All(
		datastore.Eq("status", "open"),
		datastore.Gt("total", 10),
	)))
	s.Require().NoError(both, "every condition held")

	_, either := store.Put(s.ctx, key, item{Status: "cancelled"}, datastore.If(datastore.Any(
		datastore.Eq("status", "nothing-like-it"),
		datastore.Exists("status"),
	)))
	s.Require().NoError(either, "one of them held")

	_, neither := store.Put(s.ctx, key, item{}, datastore.If(datastore.All(
		datastore.Exists("status"),
		datastore.Eq("status", "nothing-like-it"),
	)))
	s.ErrorIs(neither, datastore.ErrConditionFailed, "one did not, so the group did not")
}

func (s *DoublesTestSuite) Test_contains_matches_a_substring_and_a_member() {
	store := s.store()
	key := datastore.Key{Partition: "o-1"}
	s.Require().NoError(store.Seed(key, map[string]any{
		"status": "part-paid",
		"tags":   []string{"urgent", "eu"},
	}))

	_, text := store.Put(s.ctx, key, item{}, datastore.If(datastore.Contains("status", "paid")))
	s.Require().NoError(text, "a substring of a string")

	s.Require().NoError(store.Seed(key, map[string]any{
		"status": "part-paid",
		"tags":   []string{"urgent", "eu"},
	}))
	_, member := store.Put(s.ctx, key, item{}, datastore.If(datastore.Contains("tags", "eu")))
	s.Require().NoError(member, "or a member of a list")
}

// --- queue and topic ---

func (s *DoublesTestSuite) Test_a_batch_send_accounts_for_every_entry() {
	q := celeritytest.NewQueue("work")

	result, err := q.SendBatch(s.ctx, []queue.BatchEntry{
		{ID: "a", Body: []byte("one")},
		{ID: "b", Body: []byte("two")},
	})

	s.Require().NoError(err)
	s.Len(result.Successful, 2)
	s.Empty(result.Failed)
	s.Empty(result.Unsent)
	s.Equal(2, q.Len())
}

func (s *DoublesTestSuite) Test_a_batch_send_that_did_not_complete_leaves_every_entry_unsent() {
	// A request that did not complete leaves the entries to retry, which is
	// what Unsent is for.
	q := celeritytest.NewQueue("work")
	q.Refuse = errors.New("the queue is full")

	result, err := q.SendBatch(s.ctx, []queue.BatchEntry{
		{ID: "a", Body: []byte("one")},
		{ID: "b", Body: []byte("two")},
	})

	s.Require().Error(err)
	s.Empty(result.Successful)
	s.Len(result.Unsent, 2)
	s.Equal(0, q.Len())
}

func (s *DoublesTestSuite) Test_a_refusal_fires_once_and_then_clears() {
	q := celeritytest.NewQueue("work")
	q.Refuse = errors.New("the queue is full")

	_, first := q.Send(s.ctx, []byte("one"))
	_, second := q.Send(s.ctx, []byte("two"))

	s.Require().Error(first)
	s.Require().NoError(second, "so a test arranging one does not have to clear it")
	s.Equal(1, q.Len())
}

func (s *DoublesTestSuite) Test_a_batch_publish_carries_each_entrys_own_options() {
	// A batch commonly spans subjects, so the options are the entry's rather
	// than the batch's.
	topics := celeritytest.NewTopic("events")

	_, err := topics.PublishBatch(s.ctx, []topic.BatchEntry{
		{ID: "a", Body: []byte("one"), Options: []topic.SendOption{
			func(o *topic.SendOptions) { o.Subject = "An order was created" },
		}},
		{ID: "b", Body: []byte("two")},
	})

	s.Require().NoError(err)
	published := topics.Published()
	s.Require().Len(published, 2)
	s.Equal("An order was created", published[0].Options.Subject)
	s.Empty(published[1].Options.Subject)
}

func (s *DoublesTestSuite) Test_resetting_forgets_what_was_sent() {
	q := celeritytest.NewQueue("work")
	_, err := q.Send(s.ctx, []byte("one"))
	s.Require().NoError(err)

	q.Reset()

	s.Equal(0, q.Len())
}

// --- bucket ---

func (s *DoublesTestSuite) Test_an_object_written_is_read_back_with_what_was_recorded() {
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "a.txt", strings.NewReader("hello"),
		func(o *bucket.PutOptions) { o.ContentType = "text/plain" })
	s.Require().NoError(err)

	object, err := store.Get(s.ctx, "a.txt")
	s.Require().NoError(err)
	defer object.Body.Close()

	body, err := io.ReadAll(object.Body)
	s.Require().NoError(err)
	s.Equal("hello", string(body))
	s.Equal("text/plain", object.Info.ContentType)
	s.EqualValues(5, object.Info.Size)
	s.EqualValues(5, object.ContentLength)
}

func (s *DoublesTestSuite) Test_a_ranged_read_reports_the_whole_size_and_the_part_s_length() {
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "a.txt", strings.NewReader("0123456789"))
	s.Require().NoError(err)

	cases := []struct {
		name   string
		asked  bucket.Range
		want   string
		length int64
	}{
		{"a length from a start", bucket.Range{Start: 2, Length: 3}, "234", 3},
		{"a start to the end", bucket.Range{Start: 8}, "89", 2},
		{"a length past the end", bucket.Range{Start: 8, Length: 100}, "89", 2},
	}

	for _, test := range cases {
		s.Run(test.name, func() {
			object, err := store.Get(
				s.ctx, "a.txt",
				func(o *bucket.GetOptions) {
					o.Range = test.asked
				},
			)
			s.Require().NoError(err)
			defer object.Body.Close()

			body, err := io.ReadAll(object.Body)
			s.Require().NoError(err)
			s.Equal(test.want, string(body))
			s.EqualValues(10, object.Info.Size, "the whole object's")
			s.EqualValues(test.length, object.ContentLength, "and the part that came back")
		})
	}
}

func (s *DoublesTestSuite) Test_a_delete_hides_the_versions_and_naming_one_removes_it() {
	store := celeritytest.NewBucket("uploads")
	first, err := store.Put(s.ctx, "a.txt", strings.NewReader("one"))
	s.Require().NoError(err)
	_, err = store.Put(s.ctx, "a.txt", strings.NewReader("two"))
	s.Require().NoError(err)

	s.Require().NoError(store.Delete(s.ctx, "a.txt"))

	_, err = store.Get(s.ctx, "a.txt")
	s.ErrorIs(err, bucket.ErrNotFound, "a read stops finding it")

	held, err := store.Get(s.ctx, "a.txt", func(o *bucket.GetOptions) { o.VersionID = first.VersionID })
	s.Require().NoError(err, "while the versions under the marker are still there")
	defer held.Body.Close()
	body, _ := io.ReadAll(held.Body)
	s.Equal("one", string(body))
}

func (s *DoublesTestSuite) Test_a_version_listing_carries_the_markers_newest_first() {
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "a.txt", strings.NewReader("one"))
	s.Require().NoError(err)
	s.Require().NoError(store.Delete(s.ctx, "a.txt"))

	versions, _, err := store.Versions(s.ctx, "")

	s.Require().NoError(err)
	s.Require().Len(versions, 2)
	s.True(versions[0].DeleteMarker, "the marker is newest")
	s.True(versions[0].Current)
	s.False(versions[1].DeleteMarker)
}

func (s *DoublesTestSuite) Test_a_read_of_a_delete_marker_is_an_absence() {
	// Which is not obvious from what the real stores answer, and is what a
	// caller passing an id back from a version listing will hit.
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "a.txt", strings.NewReader("one"))
	s.Require().NoError(err)
	s.Require().NoError(store.Delete(s.ctx, "a.txt"))

	versions, _, err := store.Versions(s.ctx, "")
	s.Require().NoError(err)

	_, err = store.Get(s.ctx, "a.txt",
		func(o *bucket.GetOptions) { o.VersionID = versions[0].VersionID })

	s.ErrorIs(err, bucket.ErrNotFound)
}

func (s *DoublesTestSuite) Test_deleting_a_key_that_holds_nothing_is_not_a_failure() {
	s.NoError(celeritytest.NewBucket("uploads").Delete(s.ctx, "never-there.txt"))
}

func (s *DoublesTestSuite) Test_a_copy_into_another_bucket_writes_there_and_not_here() {
	source := celeritytest.NewBucket("uploads")
	destination := celeritytest.NewBucket("archive")
	_, err := source.Put(s.ctx, "a.txt", strings.NewReader("one"))
	s.Require().NoError(err)

	_, err = source.Copy(s.ctx, "a.txt", bucket.Destination{Key: "b.txt", Store: destination})

	s.Require().NoError(err)
	s.Equal([]string{"a.txt"}, source.Keys())
	s.Equal([]string{"b.txt"}, destination.Keys())
}

func (s *DoublesTestSuite) Test_a_listing_reports_keys_and_sizes_rather_than_headers() {
	// A store answers a listing with the keys and their sizes rather than each
	// object's headers, so a handler wanting the type has to ask for it.
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "invoices/a.txt", strings.NewReader("one"),
		func(o *bucket.PutOptions) { o.ContentType = "text/plain" })
	s.Require().NoError(err)
	_, err = store.Put(s.ctx, "other/b.txt", strings.NewReader("two"))
	s.Require().NoError(err)

	listed, cursor, err := store.List(s.ctx, "invoices/")

	s.Require().NoError(err)
	s.Require().Len(listed, 1, "only what is under the prefix")
	s.Equal("invoices/a.txt", listed[0].Key)
	s.EqualValues(3, listed[0].Size)
	s.Empty(listed[0].ContentType, "which a listing does not carry")
	s.False(cursor.More())
}

func (s *DoublesTestSuite) Test_a_listing_pages_and_resumes() {
	store := celeritytest.NewBucket("uploads")
	for _, key := range []string{"a", "b", "c"} {
		_, err := store.Put(s.ctx, key, strings.NewReader(key))
		s.Require().NoError(err)
	}

	first, cursor, err := store.List(s.ctx, "", func(o *bucket.ListOptions) { o.Limit = 2 })
	s.Require().NoError(err)
	s.Require().Len(first, 2)
	s.Require().True(cursor.More())

	second, next, err := store.List(s.ctx, "", func(o *bucket.ListOptions) {
		o.Limit = 2
		o.Cursor = string(cursor)
	})
	s.Require().NoError(err)
	s.Len(second, 1)
	s.False(next.More())
}

func (s *DoublesTestSuite) Test_exists_asks_about_the_current_version_or_a_named_one() {
	store := celeritytest.NewBucket("uploads")
	written, err := store.Put(s.ctx, "a.txt", strings.NewReader("one"))
	s.Require().NoError(err)
	s.Require().NoError(store.Delete(s.ctx, "a.txt"))

	current, err := store.Exists(s.ctx, "a.txt")
	s.Require().NoError(err)
	s.False(current, "the newest version is a marker")

	named, err := store.Exists(s.ctx, "a.txt",
		func(o *bucket.ExistsOptions) { o.VersionID = written.VersionID })
	s.Require().NoError(err)
	s.True(named, "while the version under it is still there")
}

func (s *DoublesTestSuite) Test_exists_is_false_rather_than_an_absence() {
	// The question was whether it is there, and no is an answer.
	held, err := celeritytest.NewBucket("uploads").Exists(s.ctx, "never-there")

	s.Require().NoError(err)
	s.False(held)
}

func (s *DoublesTestSuite) Test_info_reads_what_is_recorded_without_fetching_the_object() {
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "a.txt", strings.NewReader("hello"),
		func(o *bucket.PutOptions) { o.Metadata = map[string]string{"source": "orders"} })
	s.Require().NoError(err)

	info, err := store.Info(s.ctx, "a.txt")

	s.Require().NoError(err)
	s.EqualValues(5, info.Size)
	s.Equal("orders", info.Metadata["source"])
}

func (s *DoublesTestSuite) Test_deleting_many_accounts_for_every_reference() {
	store := celeritytest.NewBucket("uploads")
	for _, key := range []string{"a", "b"} {
		_, err := store.Put(s.ctx, key, strings.NewReader(key))
		s.Require().NoError(err)
	}

	result, err := store.DeleteMany(s.ctx, []bucket.ObjectRef{
		{Key: "a"}, {Key: "b"}, {Key: "never-there"},
	})

	s.Require().NoError(err)
	s.Len(result.Deleted, 3, "a key that held nothing is not a failure")
	s.Empty(result.Failed)
	s.Empty(result.Unsent)
	s.Empty(store.Keys())
}

func (s *DoublesTestSuite) Test_what_a_handler_wrote_is_readable_without_going_through_the_store() {
	store := celeritytest.NewBucket("uploads")
	_, err := store.Put(s.ctx, "a.txt", strings.NewReader("hello"))
	s.Require().NoError(err)

	body, held := store.Contents("a.txt")

	s.Require().True(held)
	s.Equal("hello", string(body))
	s.Equal(1, store.Len())
}

func (s *DoublesTestSuite) Test_a_signed_url_names_the_bucket_the_key_and_the_action() {
	store := celeritytest.NewBucket("uploads")

	signed, err := store.SignedURL(s.ctx, "a.txt", bucket.SignWrite, time.Hour)

	s.Require().NoError(err)
	s.Contains(signed.URL, "uploads")
	s.Contains(signed.URL, "a.txt")
	s.Contains(signed.URL, string(bucket.SignWrite))
	s.False(signed.ExpiresAt.IsZero())
}

// --- stubs ---

func (s *DoublesTestSuite) Test_a_cache_call_a_test_did_not_arrange_says_so() {
	stub := celeritytest.NewCacheStub("sessions")

	_, err := stub.Get(s.ctx, "key")

	s.Require().Error(err)
	s.Contains(err.Error(), "sessions")
	s.Contains(err.Error(), "Get", "naming the call the test did not account for")
	s.Equal([]string{"Get"}, stub.Calls())
}

func (s *DoublesTestSuite) Test_a_cache_answers_what_a_test_arranged() {
	stub := celeritytest.NewCacheStub("sessions")
	stub.GetFunc = func(ctx context.Context, key string) (string, error) {
		return "arranged", nil
	}

	value, err := stub.Get(s.ctx, "key")

	s.Require().NoError(err)
	s.Equal("arranged", value)
	s.True(stub.Called("Get"))
}

func (s *DoublesTestSuite) Test_a_database_no_test_supplied_refuses_and_says_how_to_test_it() {
	// Rather than recording the call and answering a nil connection, which is
	// a test that would pass with the query misspelt.
	res := celeritytest.Resources()

	database, err := res.SQLDatabase(resources.Ref{
		Kind: resources.KindSQLDatabase, Name: "ordersDb"})
	s.Require().NoError(err, "a handle is still taken; it is the call that refuses")

	for _, endpoint := range []struct {
		name string
		call func() (sqldb.Conn, error)
	}{
		{"Writer", func() (sqldb.Conn, error) { return database.Writer(s.ctx) }},
		{"Reader", func() (sqldb.Conn, error) { return database.Reader(s.ctx) }},
	} {
		s.Run(endpoint.name, func() {
			_, err := endpoint.call()

			s.Require().Error(err)
			s.ErrorContains(err, "ordersDb", "naming the resource")
			s.ErrorContains(err, "celerity dev", "naming where an engine comes from")
			s.ErrorContains(err, "WithDatabase", "naming how to hand one over")
		})
	}
}

func (s *DoublesTestSuite) Test_a_database_a_test_supplied_is_what_the_handler_reaches() {
	// The supported way to test SQL is a real engine, so the provider takes one
	// rather than offering something to arrange.
	supplied := suppliedDatabase{}
	res := celeritytest.Resources().WithDatabase("ordersDb", supplied)

	database, err := res.SQLDatabase(resources.Ref{
		Kind: resources.KindSQLDatabase, Name: "ordersDb"})
	s.Require().NoError(err)

	_, err = database.Writer(s.ctx)
	s.ErrorIs(err, errSupplied, "the test's own client, not a refusal")
}

var errSupplied = errors.New("the test's own database")

// suppliedDatabase stands in for the engine a suite brings up, which is what
// WithDatabase is for.
type suppliedDatabase struct{}

func (suppliedDatabase) Writer(context.Context) (sqldb.Conn, error) {
	return nil, errSupplied
}

func (suppliedDatabase) Reader(context.Context) (sqldb.Conn, error) {
	return nil, errSupplied
}
