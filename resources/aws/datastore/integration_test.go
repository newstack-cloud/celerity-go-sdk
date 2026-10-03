//go:build integration

// The DynamoDB client against the service it calls.
//
// This covers what the suites alongside it cannot establish, that a request is shaped the
// way DynamoDB expects. A stand-in agrees with whatever this package sends it,
// so addressing an item by the key a table declared, resuming a query on an
// index, projecting, batching and rolling an atomic write back are all things
// only the service can truly answer.
package datastore_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	awsresources "github.com/newstack-cloud/celerity-go-sdk/resources/aws"
	_ "github.com/newstack-cloud/celerity-go-sdk/resources/aws/datastore"
	"github.com/newstack-cloud/celerity-go-sdk/resources/aws/internal/awstest"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

const (
	tableBaseName = "orders-integration"
	statusIndex   = "byStatus"
)

type DatastoreIntegrationTestSuite struct {
	suite.Suite

	target    awstest.Target
	provider  *awsresources.Provider
	dynamo    *dynamodb.Client
	tableName string
}

func TestDatastoreIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(DatastoreIntegrationTestSuite))
}

func (s *DatastoreIntegrationTestSuite) SetupSuite() {
	var cfg aws.Config
	s.target, cfg = awstest.AWS(s.T())
	s.tableName = s.target.Name(tableBaseName)
	s.dynamo = dynamodb.NewFromConfig(cfg)

	s.target.Reachable(s.T(), func(ctx context.Context) error {
		_, err := s.dynamo.ListTables(ctx, &dynamodb.ListTablesInput{})
		return err
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.createTable(ctx)
	s.emptyTable(ctx)
	s.provider = awsresources.New()
}

// emptyTable is what makes a second run mean the same as the first. The table
// is created once and kept, which is cheap and is what a deployment does, so
// the items a previous run wrote are still in it. Against an emulator the
// containers are usually thrown away and this does nothing; against an account
// it is the whole of the reset.
func (s *DatastoreIntegrationTestSuite) emptyTable(ctx context.Context) {
	for {
		page, err := s.dynamo.Scan(ctx, &dynamodb.ScanInput{
			TableName:            aws.String(s.tableName),
			ProjectionExpression: aws.String("customerId, orderId"),
		})
		s.Require().NoError(err, "reading the items a previous run left")
		s.deleteAll(ctx, page.Items)
		if len(page.LastEvaluatedKey) == 0 {
			return
		}
	}
}

func (s *DatastoreIntegrationTestSuite) deleteAll(ctx context.Context, keys []map[string]dynamotypes.AttributeValue) {
	const perRequest = 25
	for start := 0; start < len(keys); start += perRequest {
		end := min(start+perRequest, len(keys))
		writes := make([]dynamotypes.WriteRequest, 0, end-start)
		for _, key := range keys[start:end] {
			writes = append(writes, dynamotypes.WriteRequest{
				DeleteRequest: &dynamotypes.DeleteRequest{Key: key},
			})
		}
		_, err := s.dynamo.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]dynamotypes.WriteRequest{s.tableName: writes},
		})
		s.Require().NoError(err, "clearing the items a previous run left")
	}
}

func (s *DatastoreIntegrationTestSuite) createTable(ctx context.Context) {
	_, err := s.dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(s.tableName),
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("customerId"), AttributeType: dynamotypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("orderId"), AttributeType: dynamotypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("status"), AttributeType: dynamotypes.ScalarAttributeTypeS},
		},
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("customerId"), KeyType: dynamotypes.KeyTypeHash},
			{AttributeName: aws.String("orderId"), KeyType: dynamotypes.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []dynamotypes.GlobalSecondaryIndex{{
			IndexName: aws.String(statusIndex),
			KeySchema: []dynamotypes.KeySchemaElement{
				{AttributeName: aws.String("status"), KeyType: dynamotypes.KeyTypeHash},
				{AttributeName: aws.String("orderId"), KeyType: dynamotypes.KeyTypeRange},
			},
			Projection: &dynamotypes.Projection{ProjectionType: dynamotypes.ProjectionTypeAll},
		}},
		BillingMode: dynamotypes.BillingModePayPerRequest,
	})
	if err != nil {
		var exists *dynamotypes.ResourceInUseException
		s.Require().ErrorAs(err, &exists, "creating the table this suite reads")
	}
}

// The data store every case here drives, named the way a blueprint
// names one.
func (s *DatastoreIntegrationTestSuite) store() datastore.Client {
	store, err := s.provider.Datastore(
		awstest.SimpleRef(resources.KindDatastore, "ordersTable", s.tableName),
	)
	s.Require().NoError(err)
	return store
}

// ignoreRevision drops the revision a read or a write returns, for the
// assertions that are about something else.
func ignoreRevision(_ datastore.Revision, err error) error {
	return err
}

func (s *DatastoreIntegrationTestSuite) Test_an_item_is_addressed_by_the_key_the_table_declared() {
	// The key schema is read from the table rather than told to the handler,
	// and only the service can say whether what was read addresses an item.
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-1", Sort: "o-1"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, order{Total: 42})))

	var got order
	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &got)))
	s.Equal(order{CustomerID: "c-1", OrderID: "o-1", Total: 42}, got,
		"the key should have been written over the item")

	s.Require().NoError(ignoreRevision(store.Put(ctx,
		datastore.Key{Partition: "c-1", Sort: "o-2"}, order{Total: 7})))

	var found []order
	_, err := store.Query(ctx, datastore.Query{Partition: "c-1"}, &found)
	s.Require().NoError(err)
	s.Len(found, 2)

	s.Require().NoError(store.Delete(ctx, key))
	_, err = store.Get(ctx, key, &got)
	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrNotFound)
}

func (s *DatastoreIntegrationTestSuite) Test_an_item_carrying_a_different_key_is_refused() {
	// A put replaces what is under a key rather than moving an item between
	// keys. Overwriting the item's own value silently would discard the change
	// and report that the write succeeded, so it is refused instead.
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "conflict-1", Sort: "o-1"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, key,
		order{CustomerID: "conflict-1", OrderID: "o-1", Total: 1})))

	var stored order
	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &stored)))
	stored.CustomerID = "conflict-2"

	_, err := store.Put(ctx, key, stored)

	s.Require().Error(err, "the key the item claims is not the key it is under")
	s.Contains(err.Error(), "conflict-2")

	// Nothing moved, and nothing was lost.
	var after order
	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &after)))
	s.Equal("conflict-1", after.CustomerID)
	s.Equal(1, after.Total)

	_, err = store.Get(ctx, datastore.Key{Partition: "conflict-2", Sort: "o-1"}, &after)
	s.Require().ErrorIs(err, datastore.ErrNotFound,
		"and nothing was written where the item said it belonged")
}

func (s *DatastoreIntegrationTestSuite) Test_an_item_read_and_written_back_agrees_with_its_key() {
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "rmw-1", Sort: "o-1"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, order{Total: 1})))

	var held order
	revision, err := store.Get(ctx, key, &held)
	s.Require().NoError(err)
	s.Equal("rmw-1", held.CustomerID, "the read filled the key fields")

	held.Total = 2
	_, err = store.Put(ctx, key, held, datastore.IfUnchanged(revision))
	s.Require().NoError(err, "writing back what was read is not a conflict")

	var after order
	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &after)))
	s.Equal(2, after.Total)
}

func (s *DatastoreIntegrationTestSuite) Test_a_query_on_an_index_resumes_from_a_cursor() {
	// The case a typed key cannot express. DynamoDB wants the index's key and
	// the table's in a position, and refuses one carrying only the index's, so
	// this is what says the cursor carries what the service asked for.
	ctx := context.Background()
	store := s.store()

	for _, id := range []string{"i-1", "i-2", "i-3"} {
		s.Require().NoError(ignoreRevision(store.Put(ctx,
			datastore.Key{Partition: "c-indexed", Sort: id},
			indexed{Status: "open"})))
	}

	query := datastore.Query{Partition: "open", Index: statusIndex, Limit: 2}

	var first []indexed
	cursor, err := store.Query(ctx, query, &first)
	s.Require().NoError(err)
	s.Require().Len(first, 2)
	s.Require().True(cursor.More(), "there is a third item, so there is more to come")

	query.Cursor = string(cursor)
	var second []indexed
	_, err = store.Query(ctx, query, &second)

	s.Require().NoError(err)
	s.Require().Len(second, 1)
	s.Equal("i-3", second[0].OrderID)

	// And read through, which is the ordinary way a handler takes the lot.
	var ids []string
	for item, err := range datastore.Items[indexed](ctx, store, datastore.Query{
		Partition: "open", Index: statusIndex, Limit: 2,
	}) {
		s.Require().NoError(err)
		ids = append(ids, item.OrderID)
	}
	s.Equal([]string{"i-1", "i-2", "i-3"}, ids)
}

func (s *DatastoreIntegrationTestSuite) Test_a_query_reads_only_the_part_of_a_partition_asked_for() {
	// Only the service can say that a sort condition narrowed the read rather
	// than the answer. A stand-in returns whatever it was given.
	ctx := context.Background()
	store := s.store()

	for _, id := range []string{"2026-01-05", "2026-01-20", "2026-02-10", "2026-03-01"} {
		s.Require().NoError(ignoreRevision(store.Put(
			ctx,
			datastore.Key{Partition: "c-sorted", Sort: id},
			indexed{Status: "open"},
		)))
	}

	cases := []struct {
		name string
		sort *datastore.SortCondition
		want []string
	}{
		{
			name: "within a range",
			sort: datastore.SortBetween("2026-01-01", "2026-02-28"),
			want: []string{"2026-01-05", "2026-01-20", "2026-02-10"},
		},
		{
			name: "beginning with a prefix",
			sort: datastore.SortStartsWith("2026-01"),
			want: []string{"2026-01-05", "2026-01-20"},
		},
		{
			name: "at or above",
			sort: datastore.SortGreaterOrEqual("2026-02-10"),
			want: []string{"2026-02-10", "2026-03-01"},
		},
		{
			name: "one exactly",
			sort: datastore.SortEqual("2026-01-20"),
			want: []string{"2026-01-20"},
		},
		{
			name: "below",
			sort: datastore.SortLess("2026-01-20"),
			want: []string{"2026-01-05"},
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			var found []indexed
			_, err := store.Query(ctx, datastore.Query{
				Partition: "c-sorted",
				Sort:      tc.sort,
			}, &found)

			s.Require().NoError(err)
			ids := make([]string, 0, len(found))
			for _, item := range found {
				ids = append(ids, item.OrderID)
			}
			s.Equal(tc.want, ids)
		})
	}
}

func (s *DatastoreIntegrationTestSuite) Test_a_conditional_write_is_how_two_handlers_do_not_lose_an_update() {
	// The case the whole of this exists for: read, decide, write, and find out
	// rather than not that something else got there first.
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-conditional", Sort: "o-1"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Version: 1, Total: 10})))

	// The write that read version 1 and is putting its change back.
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Version: 2, Total: 20},
		datastore.If(datastore.Eq("version", 1)))))

	// A second write that also read version 1, arriving late. Nothing about the
	// request says it is late; the condition is what makes it refusable.
	_, err := store.Put(ctx, key, versioned{Version: 2, Total: 99},
		datastore.If(datastore.Eq("version", 1)))

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed)

	var got versioned
	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &got)))
	s.Equal(20, got.Total, "the late write should not have landed")
}

func (s *DatastoreIntegrationTestSuite) Test_an_insert_that_must_not_overwrite() {
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-insert", Sort: "o-1"}
	create := datastore.If(datastore.NotExists("orderId"))

	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Total: 10}, create)))

	_, err := store.Put(ctx, key, versioned{Total: 99}, create)

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed)

	var got versioned
	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &got)))
	s.Equal(10, got.Total, "the first write should stand")
}

func (s *DatastoreIntegrationTestSuite) Test_a_conditional_delete_and_a_nested_condition() {
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-delete", Sort: "o-1"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Version: 1, Total: 10})))

	// Nested and-or, which is where the precedence of a hand-built expression
	// would be wrong in a way only a wrong answer would reveal.
	unwanted := datastore.All(
		datastore.Eq("version", 1),
		datastore.Any(datastore.Gt("total", 1000), datastore.NotExists("paidAt")),
	)
	s.Require().NoError(store.Delete(ctx, key, datastore.If(unwanted)))

	// And once it is gone, the same condition cannot hold.
	err := store.Delete(ctx, key, datastore.If(unwanted))

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed)
}

func (s *DatastoreIntegrationTestSuite) Test_a_revision_is_how_two_handlers_do_not_lose_an_update() {
	// The same race as the conditional write above, but without the item
	// needing a version attribute of its own, which is what makes it portable.
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-revision", Sort: "o-1"}
	written, err := store.Put(ctx, key, versioned{Total: 10})
	s.Require().NoError(err)

	var got versioned
	read, err := store.Get(ctx, key, &got)
	s.Require().NoError(err)
	s.Equal(written, read, "a read should report the revision the write stamped")

	// The handler that read, decided, and is writing back.
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Total: 20},
		datastore.IfUnchanged(read))))

	// A second handler that also read that revision, arriving late.
	_, err = store.Put(ctx, key, versioned{Total: 99}, datastore.IfUnchanged(read))

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed)

	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &got)))
	s.Equal(20, got.Total, "the late write should not have landed")
}

func (s *DatastoreIntegrationTestSuite) Test_an_item_written_without_a_revision_can_still_be_updated() {
	// Every table has items that predate revisions. Reading one gives
	// Unrevisioned, and writing with it is a real precondition rather than none.
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-legacy", Sort: "o-1"}
	s.Require().NoError(s.putWithoutRevision(ctx, key, 10))

	var got versioned
	read, err := store.Get(ctx, key, &got)
	s.Require().NoError(err)
	_, carried := read.Value()
	s.False(carried, "an item written outside the SDK carries no revision")

	// Writing with it succeeds, and stamps one.
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Total: 20},
		datastore.IfUnchanged(read))))

	// The same precondition now fails, because the item is no longer
	// unrevisioned. Without that, a stale handler could overwrite silently.
	_, err = store.Put(ctx, key, versioned{Total: 99}, datastore.IfUnchanged(read))

	s.Require().Error(err)
	s.ErrorIs(err, datastore.ErrConditionFailed)

	s.Require().NoError(ignoreRevision(store.Get(ctx, key, &got)))
	s.Equal(20, got.Total)
}

func (s *DatastoreIntegrationTestSuite) Test_a_query_filters_projects_and_reverses() {
	// A filter, a projection and a direction are all the service's own work, so
	// only the service can say whether the expressions built for them are right.
	ctx := context.Background()
	store := s.store()

	for i, id := range []string{"o-1", "o-2", "o-3"} {
		s.Require().NoError(ignoreRevision(store.Put(ctx,
			datastore.Key{Partition: "c-narrowed", Sort: id},
			versioned{Version: i, Total: (i + 1) * 10})))
	}

	s.Run("a filter drops what does not match", func() {
		var found []versioned
		_, err := store.Query(ctx, datastore.Query{
			Partition: "c-narrowed",
			Filter:    ptr(datastore.Gt("total", 10)),
		}, &found)

		s.Require().NoError(err)
		s.Len(found, 2, "the 10 should have been filtered out")
	})

	s.Run("a projection returns only what was asked for, plus the key", func() {
		var found []map[string]any
		_, err := store.Query(ctx, datastore.Query{
			Partition: "c-narrowed",
			Project:   []string{"total"},
		}, &found)

		s.Require().NoError(err)
		s.Require().NotEmpty(found)
		s.Contains(found[0], "total")
		s.Contains(found[0], "customerId", "the key comes back whether or not it was listed")
		s.Contains(found[0], "orderId")
		s.NotContains(found[0], "version", "an unlisted field should not have been read")
		s.NotContains(found[0], datastore.RevisionField,
			"the revision is taken off even when the projection asked for it")
	})

	s.Run("descending reads the partition from the other end", func() {
		var found []versioned
		_, err := store.Query(ctx, datastore.Query{
			Partition:  "c-narrowed",
			Descending: true,
		}, &found)

		s.Require().NoError(err)
		s.Require().Len(found, 3)
		s.Equal(30, found[0].Total, "the last sort value should come first")
		s.Equal(10, found[2].Total)
	})
}

func (s *DatastoreIntegrationTestSuite) Test_a_scan_reads_the_whole_table_a_page_at_a_time() {
	ctx := context.Background()
	store := s.store()

	for i, id := range []string{"s-1", "s-2", "s-3"} {
		s.Require().NoError(ignoreRevision(store.Put(ctx,
			datastore.Key{Partition: "c-scanned", Sort: id},
			versioned{Version: i, Total: (i + 1) * 10})))
	}

	s.Run("a page is one request, and the cursor resumes the next", func() {
		var first []versioned
		cursor, err := store.Scan(ctx, datastore.Scan{Limit: 1}, &first)

		s.Require().NoError(err)
		s.Len(first, 1)
		s.Require().True(cursor.More())

		var second []versioned
		_, err = store.Scan(ctx, datastore.Scan{Limit: 1, Cursor: string(cursor)}, &second)
		s.Require().NoError(err)
		s.Len(second, 1)
		s.NotEqual(first[0], second[0], "resuming should not re-read the first item")
	})

	s.Run("a filter is applied by the service", func() {
		var found []versioned
		_, err := store.Scan(ctx, datastore.Scan{
			Filter: ptr(datastore.Gt("total", 1000)),
		}, &found)

		s.Require().NoError(err)
		s.Empty(found, "nothing in the table totals over a thousand")
	})

	s.Run("reading through gathers every page", func() {
		seen := map[int]bool{}
		for item, err := range datastore.Scanned[versioned](ctx, store, datastore.Scan{Limit: 1}) {
			s.Require().NoError(err)
			seen[item.Total] = true
		}

		// The table holds items other tests wrote, so this asserts that the
		// ones written here were all reached rather than counting the whole.
		s.True(seen[10] && seen[20] && seen[30])
	})
}

func (s *DatastoreIntegrationTestSuite) Test_a_batch_reads_and_writes_many_items_at_once() {
	// Chunking, the shape of a batch request and what comes back from one are
	// all the service's own, so only the service can say whether they are right.
	ctx := context.Background()
	store := s.store()

	// Deliberately over one write request's worth, so the split is exercised.
	ops := make([]datastore.BatchOp, 30)
	wanted := make([]datastore.Key, 30)
	for i := range ops {
		key := datastore.Key{Partition: "c-batched", Sort: fmt.Sprintf("o-%02d", i)}
		ops[i] = datastore.PutOp(key, versioned{Version: i, Total: i})
		wanted[i] = key
	}

	missed, err := store.BatchWrite(ctx, ops)
	s.Require().NoError(err)
	s.Empty(missed, "AWS or emulator should not be throttling a batch of thirty")

	s.Run("every item written in the batch can be read back", func() {
		var found []versioned
		unfetched, err := store.BatchGet(ctx, wanted, &found)

		s.Require().NoError(err)
		s.Empty(unfetched)
		s.Len(found, 30)
	})

	s.Run("a batch put leaves an item revisioned like any other write", func() {
		var got versioned
		revision, err := store.Get(ctx, wanted[0], &got)

		s.Require().NoError(err)
		_, carried := revision.Value()
		s.True(carried, "a batch write must not leave an item unrevisioned")
	})

	s.Run("a key that holds nothing is absent rather than an error", func() {
		var found []versioned
		_, err := store.BatchGet(ctx, []datastore.Key{
			wanted[0],
			{Partition: "c-batched", Sort: "never-written"},
		}, &found)

		s.Require().NoError(err)
		s.Len(found, 1)
	})

	s.Run("a batch delete removes them again", func() {
		removals := make([]datastore.BatchOp, len(wanted))
		for i, key := range wanted {
			removals[i] = datastore.DeleteOp(key)
		}
		missed, err := store.BatchWrite(ctx, removals)
		s.Require().NoError(err)
		s.Empty(missed)

		var found []versioned
		_, err = store.BatchGet(ctx, wanted, &found)
		s.Require().NoError(err)
		s.Empty(found)
	})
}

func (s *DatastoreIntegrationTestSuite) Test_an_update_changes_part_of_an_item_in_place() {
	ctx := context.Background()
	store := s.store()

	key := datastore.Key{Partition: "c-updated", Sort: "o-1"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, key, versioned{Version: 1, Total: 10})))

	s.Run("set and increment leave the rest of the item alone", func() {
		_, err := store.Update(ctx, key, []datastore.Update{
			datastore.Set("status", "shipped"),
			datastore.Increment("total", 5),
		})
		s.Require().NoError(err)

		var got map[string]any
		_, err = store.Get(ctx, key, &got)
		s.Require().NoError(err)
		s.Equal("shipped", got["status"])
		s.EqualValues(15, got["total"], "the store did the addition")
		s.EqualValues(1, got["version"], "an untouched field stays as it was")
	})

	s.Run("increment starts a missing field at zero", func() {
		_, err := store.Update(ctx, key, []datastore.Update{datastore.Increment("attempts", 3)})
		s.Require().NoError(err)

		var got map[string]any
		_, err = store.Get(ctx, key, &got)
		s.Require().NoError(err)
		s.EqualValues(3, got["attempts"])
	})

	s.Run("remove deletes a field", func() {
		_, err := store.Update(ctx, key, []datastore.Update{datastore.Remove("status")})
		s.Require().NoError(err)

		var got map[string]any
		_, err = store.Get(ctx, key, &got)
		s.Require().NoError(err)
		s.NotContains(got, "status")
	})

	s.Run("an update returns a revision a later write can require", func() {
		revision, err := store.Update(ctx, key, []datastore.Update{datastore.Set("tier", "pro")})
		s.Require().NoError(err)

		_, err = store.Update(ctx, key,
			[]datastore.Update{datastore.Set("tier", "free")},
			datastore.IfUnchanged(revision))
		s.Require().NoError(err, "the revision the update returned should still be current")

		_, err = store.Update(ctx, key,
			[]datastore.Update{datastore.Set("tier", "enterprise")},
			datastore.IfUnchanged(revision))
		s.Require().Error(err)
		s.ErrorIs(err, datastore.ErrConditionFailed, "that revision is stale now")
	})

	s.Run("updating a key that holds nothing is not found, not a creation", func() {
		absent := datastore.Key{Partition: "c-updated", Sort: "never-written"}

		_, err := store.Update(ctx, absent, []datastore.Update{datastore.Set("status", "new")})

		s.Require().Error(err)
		s.ErrorIs(err, datastore.ErrNotFound,
			"DynamoDB would have created it, which is what the contract refuses")

		var got versioned
		_, err = store.Get(ctx, absent, &got)
		s.ErrorIs(err, datastore.ErrNotFound, "and nothing should have been created")
	})
}

func (s *DatastoreIntegrationTestSuite) Test_an_atomic_write_applies_everything_or_nothing() {
	// All or none is the whole point, and only the service can demonstrate it.
	ctx := context.Background()
	store := s.store()

	order := datastore.Key{Partition: "c-atomic", Sort: "o-1"}
	counter := datastore.Key{Partition: "c-atomic", Sort: "counter"}
	s.Require().NoError(ignoreRevision(store.Put(ctx, counter, versioned{Total: 0})))

	s.Run("every operation lands together", func() {
		err := store.Atomically(ctx, "c-atomic", []datastore.AtomicOp{
			datastore.AtomicPut(order, versioned{Version: 1, Total: 50}),
			datastore.AtomicUpdate(counter, []datastore.Update{datastore.Increment("total", 1)}),
		})
		s.Require().NoError(err)

		var placed, counted versioned
		_, err = store.Get(ctx, order, &placed)
		s.Require().NoError(err)
		s.Equal(50, placed.Total)

		_, err = store.Get(ctx, counter, &counted)
		s.Require().NoError(err)
		s.Equal(1, counted.Total)
	})

	s.Run("one refused precondition rolls the rest back", func() {
		// A revision that is not current, so the put is refused and the
		// increment beside it must not happen either.
		stale := datastore.NewRevision("not-the-current-revision")

		err := store.Atomically(ctx, "c-atomic", []datastore.AtomicOp{
			datastore.AtomicPut(order, versioned{Version: 2, Total: 99},
				datastore.IfUnchanged(stale)),
			datastore.AtomicUpdate(counter, []datastore.Update{datastore.Increment("total", 1)}),
		})

		s.Require().Error(err)
		s.ErrorIs(err, datastore.ErrConditionFailed)

		var placed, counted versioned
		_, err = store.Get(ctx, order, &placed)
		s.Require().NoError(err)
		s.Equal(50, placed.Total, "the refused put should not have landed")

		_, err = store.Get(ctx, counter, &counted)
		s.Require().NoError(err)
		s.Equal(1, counted.Total, "and the increment beside it should have rolled back")
	})

	s.Run("an update of an item that is not there refuses the write", func() {
		err := store.Atomically(ctx, "c-atomic", []datastore.AtomicOp{
			datastore.AtomicUpdate(
				datastore.Key{Partition: "c-atomic", Sort: "never-written"},
				[]datastore.Update{datastore.Set("status", "new")}),
			datastore.AtomicUpdate(counter, []datastore.Update{datastore.Increment("total", 1)}),
		})

		s.Require().Error(err)

		var counted versioned
		_, err = store.Get(ctx, counter, &counted)
		s.Require().NoError(err)
		s.Equal(1, counted.Total, "nothing beside the refused update should have applied")
	})
}

// Writes an item the way anything other than a Celerity SDK
// would with no revision attribute at all.
func (s *DatastoreIntegrationTestSuite) putWithoutRevision(
	ctx context.Context, key datastore.Key, total int,
) error {
	_, err := s.dynamo.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item: map[string]dynamotypes.AttributeValue{
			"customerId": &dynamotypes.AttributeValueMemberS{Value: key.Partition},
			"orderId":    &dynamotypes.AttributeValueMemberS{Value: key.Sort},
			"total":      &dynamotypes.AttributeValueMemberN{Value: strconv.Itoa(total)},
		},
	})
	return err
}

// versioned carries the attribute an optimistic write conditions on.
type versioned struct {
	CustomerID string `dynamodbav:"customerId"`
	OrderID    string `dynamodbav:"orderId"`
	Version    int    `dynamodbav:"version"`
	Total      int    `dynamodbav:"total"`
}

// indexed is an order carrying the attribute the secondary index is keyed on.
type indexed struct {
	CustomerID string `dynamodbav:"customerId"`
	OrderID    string `dynamodbav:"orderId"`
	Status     string `dynamodbav:"status"`
}
