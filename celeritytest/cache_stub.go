package celeritytest

import (
	"context"
	"iter"
	"sync"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/resources/cache"
)

// CacheStub records what a handler asked a cache for and answers what the test
// arranged.
//
// This is a stub rather than a fully functional cache.
// The contract is fifty-three operations
// across seven groups, and reimplementing sorted sets, hashes and expiry
// faithfully enough to be worth trusting is a large undertaking for a test utility.
// A test that needs real behaviour is better pointed at a real Redis, which
// resources/redis already reaches.
//
// A call the test did not arrange answers with an error naming it, rather than
// a zero value that turns up as a puzzle somewhere else.
type CacheStub struct {
	name string

	mu    sync.Mutex
	calls []string

	// AppendFunc answers a call to Append.
	AppendFunc func(ctx context.Context, key string, value string) (int64, error)
	// ConnectionFunc answers a call to Connection.
	ConnectionFunc func(ctx context.Context) (cache.Connection, error)
	// DecrementFunc answers a call to Decrement.
	DecrementFunc func(ctx context.Context, key string, delta int64) (int64, error)
	// DeleteFunc answers a call to Delete.
	DeleteFunc func(ctx context.Context, key string) (bool, error)
	// DeleteManyFunc answers a call to DeleteMany.
	DeleteManyFunc func(ctx context.Context, keys []string) (int64, error)
	// ExistsFunc answers a call to Exists.
	ExistsFunc func(ctx context.Context, key string) (bool, error)
	// ExpireFunc answers a call to Expire.
	ExpireFunc func(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// GetFunc answers a call to Get.
	GetFunc func(ctx context.Context, key string) (string, error)
	// GetManyFunc answers a call to GetMany.
	GetManyFunc func(ctx context.Context, keys []string) (map[string]string, error)
	// GetSetFunc answers a call to GetSet.
	GetSetFunc func(ctx context.Context, key string, value string) (string, error)
	// HashDeleteFunc answers a call to HashDelete.
	HashDeleteFunc func(ctx context.Context, key string, fields []string) (int64, error)
	// HashExistsFunc answers a call to HashExists.
	HashExistsFunc func(ctx context.Context, key string, field string) (bool, error)
	// HashGetFunc answers a call to HashGet.
	HashGetFunc func(ctx context.Context, key string, field string) (string, error)
	// HashGetAllFunc answers a call to HashGetAll.
	HashGetAllFunc func(ctx context.Context, key string) (map[string]string, error)
	// HashIncrementFunc answers a call to HashIncrement.
	HashIncrementFunc func(ctx context.Context, key string, field string, delta int64) (int64, error)
	// HashKeysFunc answers a call to HashKeys.
	HashKeysFunc func(ctx context.Context, key string) ([]string, error)
	// HashLenFunc answers a call to HashLen.
	HashLenFunc func(ctx context.Context, key string) (int64, error)
	// HashSetFunc answers a call to HashSet.
	HashSetFunc func(ctx context.Context, key string, fields map[string]string) error
	// IncrementFunc answers a call to Increment.
	IncrementFunc func(ctx context.Context, key string, delta int64) (int64, error)
	// IncrementFloatFunc answers a call to IncrementFloat.
	IncrementFloatFunc func(ctx context.Context, key string, delta float64) (float64, error)
	// ListIndexFunc answers a call to ListIndex.
	ListIndexFunc func(ctx context.Context, key string, index int64) (string, error)
	// ListLenFunc answers a call to ListLen.
	ListLenFunc func(ctx context.Context, key string) (int64, error)
	// ListPopFunc answers a call to ListPop.
	ListPopFunc func(ctx context.Context, key string, count int64, opts ...cache.EndOption) ([]string, error)
	// ListPushFunc answers a call to ListPush.
	ListPushFunc func(ctx context.Context, key string, values []string, opts ...cache.EndOption) (int64, error)
	// ListRangeFunc answers a call to ListRange.
	ListRangeFunc func(ctx context.Context, key string, start int64, stop int64) ([]string, error)
	// ListTrimFunc answers a call to ListTrim.
	ListTrimFunc func(ctx context.Context, key string, start int64, stop int64) error
	// PersistFunc answers a call to Persist.
	PersistFunc func(ctx context.Context, key string) (bool, error)
	// RenameFunc answers a call to Rename.
	RenameFunc func(ctx context.Context, key string, newKey string) error
	// ScanFunc answers a call to Scan.
	ScanFunc func(ctx context.Context, opts ...cache.ScanOption) iter.Seq2[string, error]
	// SetFunc answers a call to Set.
	SetFunc func(ctx context.Context, key string, value string, opts ...cache.SetOption) (bool, error)
	// SetAddFunc answers a call to SetAdd.
	SetAddFunc func(ctx context.Context, key string, members []string) (int64, error)
	// SetDiffFunc answers a call to SetDiff.
	SetDiffFunc func(ctx context.Context, keys []string) ([]string, error)
	// SetIntersectFunc answers a call to SetIntersect.
	SetIntersectFunc func(ctx context.Context, keys []string) ([]string, error)
	// SetIsMemberFunc answers a call to SetIsMember.
	SetIsMemberFunc func(ctx context.Context, key string, member string) (bool, error)
	// SetLenFunc answers a call to SetLen.
	SetLenFunc func(ctx context.Context, key string) (int64, error)
	// SetManyFunc answers a call to SetMany.
	SetManyFunc func(ctx context.Context, values map[string]string) error
	// SetMembersFunc answers a call to SetMembers.
	SetMembersFunc func(ctx context.Context, key string) ([]string, error)
	// SetRemoveFunc answers a call to SetRemove.
	SetRemoveFunc func(ctx context.Context, key string, members []string) (int64, error)
	// SetUnionFunc answers a call to SetUnion.
	SetUnionFunc func(ctx context.Context, keys []string) ([]string, error)
	// SortedSetAddFunc answers a call to SortedSetAdd.
	SortedSetAddFunc func(ctx context.Context, key string, members []cache.SortedSetMember) (int64, error)
	// SortedSetCountByScoreFunc answers a call to SortedSetCountByScore.
	SortedSetCountByScoreFunc func(ctx context.Context, key string, scores cache.ScoreRange) (int64, error)
	// SortedSetIncrementFunc answers a call to SortedSetIncrement.
	SortedSetIncrementFunc func(ctx context.Context, key string, member string, delta float64) (float64, error)
	// SortedSetLenFunc answers a call to SortedSetLen.
	SortedSetLenFunc func(ctx context.Context, key string) (int64, error)
	// SortedSetRangeFunc answers a call to SortedSetRange.
	SortedSetRangeFunc func(ctx context.Context, key string, start int64, stop int64, opts ...cache.RangeOption) ([]cache.SortedSetMember, error)
	// SortedSetRangeByScoreFunc answers a call to SortedSetRangeByScore.
	SortedSetRangeByScoreFunc func(ctx context.Context, key string, scores cache.ScoreRange, opts ...cache.RangeOption) ([]cache.SortedSetMember, error)
	// SortedSetRankFunc answers a call to SortedSetRank.
	SortedSetRankFunc func(ctx context.Context, key string, member string, opts ...cache.RangeOption) (int64, error)
	// SortedSetRemoveFunc answers a call to SortedSetRemove.
	SortedSetRemoveFunc func(ctx context.Context, key string, members []string) (int64, error)
	// SortedSetRemoveByRankFunc answers a call to SortedSetRemoveByRank.
	SortedSetRemoveByRankFunc func(ctx context.Context, key string, start int64, stop int64) (int64, error)
	// SortedSetRemoveByScoreFunc answers a call to SortedSetRemoveByScore.
	SortedSetRemoveByScoreFunc func(ctx context.Context, key string, scores cache.ScoreRange) (int64, error)
	// SortedSetScoreFunc answers a call to SortedSetScore.
	SortedSetScoreFunc func(ctx context.Context, key string, member string) (float64, error)
	// TTLFunc answers a call to TTL.
	TTLFunc func(ctx context.Context, key string) (time.Duration, bool, error)
	// TransactionFunc answers a call to Transaction.
	TransactionFunc func(ctx context.Context, queue func(cache.Tx)) ([]any, error)
	// TypeFunc answers a call to Type.
	TypeFunc func(ctx context.Context, key string) (cache.KeyType, error)
}

// NewCacheStub returns a stub answering nothing until a test arranges it.
func NewCacheStub(name string) *CacheStub {
	return &CacheStub{name: name}
}

// Calls returns the operations a handler asked for, in order.
func (s *CacheStub) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// Called reports whether an operation was asked for.
func (s *CacheStub) Called(name string) bool {
	for _, call := range s.Calls() {
		if call == name {
			return true
		}
	}
	return false
}

func (s *CacheStub) record(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
}

func (s *CacheStub) Append(ctx context.Context, key string, value string) (int64, error) {
	s.record("Append")
	if s.AppendFunc != nil {
		return s.AppendFunc(ctx, key, value)
	}
	return 0, notConfigured(s.name, "Append")
}

func (s *CacheStub) Connection(ctx context.Context) (cache.Connection, error) {
	s.record("Connection")
	if s.ConnectionFunc != nil {
		return s.ConnectionFunc(ctx)
	}
	return cache.Connection{}, notConfigured(s.name, "Connection")
}

func (s *CacheStub) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	s.record("Decrement")
	if s.DecrementFunc != nil {
		return s.DecrementFunc(ctx, key, delta)
	}
	return 0, notConfigured(s.name, "Decrement")
}

func (s *CacheStub) Delete(ctx context.Context, key string) (bool, error) {
	s.record("Delete")
	if s.DeleteFunc != nil {
		return s.DeleteFunc(ctx, key)
	}
	return false, notConfigured(s.name, "Delete")
}

func (s *CacheStub) DeleteMany(ctx context.Context, keys []string) (int64, error) {
	s.record("DeleteMany")
	if s.DeleteManyFunc != nil {
		return s.DeleteManyFunc(ctx, keys)
	}
	return 0, notConfigured(s.name, "DeleteMany")
}

func (s *CacheStub) Exists(ctx context.Context, key string) (bool, error) {
	s.record("Exists")
	if s.ExistsFunc != nil {
		return s.ExistsFunc(ctx, key)
	}
	return false, notConfigured(s.name, "Exists")
}

func (s *CacheStub) Expire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	s.record("Expire")
	if s.ExpireFunc != nil {
		return s.ExpireFunc(ctx, key, ttl)
	}
	return false, notConfigured(s.name, "Expire")
}

func (s *CacheStub) Get(ctx context.Context, key string) (string, error) {
	s.record("Get")
	if s.GetFunc != nil {
		return s.GetFunc(ctx, key)
	}
	return "", notConfigured(s.name, "Get")
}

func (s *CacheStub) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	s.record("GetMany")
	if s.GetManyFunc != nil {
		return s.GetManyFunc(ctx, keys)
	}
	return nil, notConfigured(s.name, "GetMany")
}

func (s *CacheStub) GetSet(ctx context.Context, key string, value string) (string, error) {
	s.record("GetSet")
	if s.GetSetFunc != nil {
		return s.GetSetFunc(ctx, key, value)
	}
	return "", notConfigured(s.name, "GetSet")
}

func (s *CacheStub) HashDelete(ctx context.Context, key string, fields []string) (int64, error) {
	s.record("HashDelete")
	if s.HashDeleteFunc != nil {
		return s.HashDeleteFunc(ctx, key, fields)
	}
	return 0, notConfigured(s.name, "HashDelete")
}

func (s *CacheStub) HashExists(ctx context.Context, key string, field string) (bool, error) {
	s.record("HashExists")
	if s.HashExistsFunc != nil {
		return s.HashExistsFunc(ctx, key, field)
	}
	return false, notConfigured(s.name, "HashExists")
}

func (s *CacheStub) HashGet(ctx context.Context, key string, field string) (string, error) {
	s.record("HashGet")
	if s.HashGetFunc != nil {
		return s.HashGetFunc(ctx, key, field)
	}
	return "", notConfigured(s.name, "HashGet")
}

func (s *CacheStub) HashGetAll(ctx context.Context, key string) (map[string]string, error) {
	s.record("HashGetAll")
	if s.HashGetAllFunc != nil {
		return s.HashGetAllFunc(ctx, key)
	}
	return nil, notConfigured(s.name, "HashGetAll")
}

func (s *CacheStub) HashIncrement(ctx context.Context, key string, field string, delta int64) (int64, error) {
	s.record("HashIncrement")
	if s.HashIncrementFunc != nil {
		return s.HashIncrementFunc(ctx, key, field, delta)
	}
	return 0, notConfigured(s.name, "HashIncrement")
}

func (s *CacheStub) HashKeys(ctx context.Context, key string) ([]string, error) {
	s.record("HashKeys")
	if s.HashKeysFunc != nil {
		return s.HashKeysFunc(ctx, key)
	}
	return nil, notConfigured(s.name, "HashKeys")
}

func (s *CacheStub) HashLen(ctx context.Context, key string) (int64, error) {
	s.record("HashLen")
	if s.HashLenFunc != nil {
		return s.HashLenFunc(ctx, key)
	}
	return 0, notConfigured(s.name, "HashLen")
}

func (s *CacheStub) HashSet(ctx context.Context, key string, fields map[string]string) error {
	s.record("HashSet")
	if s.HashSetFunc != nil {
		return s.HashSetFunc(ctx, key, fields)
	}
	return notConfigured(s.name, "HashSet")
}

func (s *CacheStub) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	s.record("Increment")
	if s.IncrementFunc != nil {
		return s.IncrementFunc(ctx, key, delta)
	}
	return 0, notConfigured(s.name, "Increment")
}

func (s *CacheStub) IncrementFloat(ctx context.Context, key string, delta float64) (float64, error) {
	s.record("IncrementFloat")
	if s.IncrementFloatFunc != nil {
		return s.IncrementFloatFunc(ctx, key, delta)
	}
	return 0, notConfigured(s.name, "IncrementFloat")
}

func (s *CacheStub) ListIndex(ctx context.Context, key string, index int64) (string, error) {
	s.record("ListIndex")
	if s.ListIndexFunc != nil {
		return s.ListIndexFunc(ctx, key, index)
	}
	return "", notConfigured(s.name, "ListIndex")
}

func (s *CacheStub) ListLen(ctx context.Context, key string) (int64, error) {
	s.record("ListLen")
	if s.ListLenFunc != nil {
		return s.ListLenFunc(ctx, key)
	}
	return 0, notConfigured(s.name, "ListLen")
}

func (s *CacheStub) ListPop(ctx context.Context, key string, count int64, opts ...cache.EndOption) ([]string, error) {
	s.record("ListPop")
	if s.ListPopFunc != nil {
		return s.ListPopFunc(ctx, key, count, opts...)
	}
	return nil, notConfigured(s.name, "ListPop")
}

func (s *CacheStub) ListPush(ctx context.Context, key string, values []string, opts ...cache.EndOption) (int64, error) {
	s.record("ListPush")
	if s.ListPushFunc != nil {
		return s.ListPushFunc(ctx, key, values, opts...)
	}
	return 0, notConfigured(s.name, "ListPush")
}

func (s *CacheStub) ListRange(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	s.record("ListRange")
	if s.ListRangeFunc != nil {
		return s.ListRangeFunc(ctx, key, start, stop)
	}
	return nil, notConfigured(s.name, "ListRange")
}

func (s *CacheStub) ListTrim(ctx context.Context, key string, start int64, stop int64) error {
	s.record("ListTrim")
	if s.ListTrimFunc != nil {
		return s.ListTrimFunc(ctx, key, start, stop)
	}
	return notConfigured(s.name, "ListTrim")
}

func (s *CacheStub) Persist(ctx context.Context, key string) (bool, error) {
	s.record("Persist")
	if s.PersistFunc != nil {
		return s.PersistFunc(ctx, key)
	}
	return false, notConfigured(s.name, "Persist")
}

func (s *CacheStub) Rename(ctx context.Context, key string, newKey string) error {
	s.record("Rename")
	if s.RenameFunc != nil {
		return s.RenameFunc(ctx, key, newKey)
	}
	return notConfigured(s.name, "Rename")
}

func (s *CacheStub) Scan(ctx context.Context, opts ...cache.ScanOption) iter.Seq2[string, error] {
	s.record("Scan")
	if s.ScanFunc != nil {
		return s.ScanFunc(ctx, opts...)
	}
	return nil
}

func (s *CacheStub) Set(ctx context.Context, key string, value string, opts ...cache.SetOption) (bool, error) {
	s.record("Set")
	if s.SetFunc != nil {
		return s.SetFunc(ctx, key, value, opts...)
	}
	return false, notConfigured(s.name, "Set")
}

func (s *CacheStub) SetAdd(ctx context.Context, key string, members []string) (int64, error) {
	s.record("SetAdd")
	if s.SetAddFunc != nil {
		return s.SetAddFunc(ctx, key, members)
	}
	return 0, notConfigured(s.name, "SetAdd")
}

func (s *CacheStub) SetDiff(ctx context.Context, keys []string) ([]string, error) {
	s.record("SetDiff")
	if s.SetDiffFunc != nil {
		return s.SetDiffFunc(ctx, keys)
	}
	return nil, notConfigured(s.name, "SetDiff")
}

func (s *CacheStub) SetIntersect(ctx context.Context, keys []string) ([]string, error) {
	s.record("SetIntersect")
	if s.SetIntersectFunc != nil {
		return s.SetIntersectFunc(ctx, keys)
	}
	return nil, notConfigured(s.name, "SetIntersect")
}

func (s *CacheStub) SetIsMember(ctx context.Context, key string, member string) (bool, error) {
	s.record("SetIsMember")
	if s.SetIsMemberFunc != nil {
		return s.SetIsMemberFunc(ctx, key, member)
	}
	return false, notConfigured(s.name, "SetIsMember")
}

func (s *CacheStub) SetLen(ctx context.Context, key string) (int64, error) {
	s.record("SetLen")
	if s.SetLenFunc != nil {
		return s.SetLenFunc(ctx, key)
	}
	return 0, notConfigured(s.name, "SetLen")
}

func (s *CacheStub) SetMany(ctx context.Context, values map[string]string) error {
	s.record("SetMany")
	if s.SetManyFunc != nil {
		return s.SetManyFunc(ctx, values)
	}
	return notConfigured(s.name, "SetMany")
}

func (s *CacheStub) SetMembers(ctx context.Context, key string) ([]string, error) {
	s.record("SetMembers")
	if s.SetMembersFunc != nil {
		return s.SetMembersFunc(ctx, key)
	}
	return nil, notConfigured(s.name, "SetMembers")
}

func (s *CacheStub) SetRemove(ctx context.Context, key string, members []string) (int64, error) {
	s.record("SetRemove")
	if s.SetRemoveFunc != nil {
		return s.SetRemoveFunc(ctx, key, members)
	}
	return 0, notConfigured(s.name, "SetRemove")
}

func (s *CacheStub) SetUnion(ctx context.Context, keys []string) ([]string, error) {
	s.record("SetUnion")
	if s.SetUnionFunc != nil {
		return s.SetUnionFunc(ctx, keys)
	}
	return nil, notConfigured(s.name, "SetUnion")
}

func (s *CacheStub) SortedSetAdd(ctx context.Context, key string, members []cache.SortedSetMember) (int64, error) {
	s.record("SortedSetAdd")
	if s.SortedSetAddFunc != nil {
		return s.SortedSetAddFunc(ctx, key, members)
	}
	return 0, notConfigured(s.name, "SortedSetAdd")
}

func (s *CacheStub) SortedSetCountByScore(ctx context.Context, key string, scores cache.ScoreRange) (int64, error) {
	s.record("SortedSetCountByScore")
	if s.SortedSetCountByScoreFunc != nil {
		return s.SortedSetCountByScoreFunc(ctx, key, scores)
	}
	return 0, notConfigured(s.name, "SortedSetCountByScore")
}

func (s *CacheStub) SortedSetIncrement(ctx context.Context, key string, member string, delta float64) (float64, error) {
	s.record("SortedSetIncrement")
	if s.SortedSetIncrementFunc != nil {
		return s.SortedSetIncrementFunc(ctx, key, member, delta)
	}
	return 0, notConfigured(s.name, "SortedSetIncrement")
}

func (s *CacheStub) SortedSetLen(ctx context.Context, key string) (int64, error) {
	s.record("SortedSetLen")
	if s.SortedSetLenFunc != nil {
		return s.SortedSetLenFunc(ctx, key)
	}
	return 0, notConfigured(s.name, "SortedSetLen")
}

func (s *CacheStub) SortedSetRange(ctx context.Context, key string, start int64, stop int64, opts ...cache.RangeOption) ([]cache.SortedSetMember, error) {
	s.record("SortedSetRange")
	if s.SortedSetRangeFunc != nil {
		return s.SortedSetRangeFunc(ctx, key, start, stop, opts...)
	}
	return nil, notConfigured(s.name, "SortedSetRange")
}

func (s *CacheStub) SortedSetRangeByScore(ctx context.Context, key string, scores cache.ScoreRange, opts ...cache.RangeOption) ([]cache.SortedSetMember, error) {
	s.record("SortedSetRangeByScore")
	if s.SortedSetRangeByScoreFunc != nil {
		return s.SortedSetRangeByScoreFunc(ctx, key, scores, opts...)
	}
	return nil, notConfigured(s.name, "SortedSetRangeByScore")
}

func (s *CacheStub) SortedSetRank(ctx context.Context, key string, member string, opts ...cache.RangeOption) (int64, error) {
	s.record("SortedSetRank")
	if s.SortedSetRankFunc != nil {
		return s.SortedSetRankFunc(ctx, key, member, opts...)
	}
	return 0, notConfigured(s.name, "SortedSetRank")
}

func (s *CacheStub) SortedSetRemove(ctx context.Context, key string, members []string) (int64, error) {
	s.record("SortedSetRemove")
	if s.SortedSetRemoveFunc != nil {
		return s.SortedSetRemoveFunc(ctx, key, members)
	}
	return 0, notConfigured(s.name, "SortedSetRemove")
}

func (s *CacheStub) SortedSetRemoveByRank(ctx context.Context, key string, start int64, stop int64) (int64, error) {
	s.record("SortedSetRemoveByRank")
	if s.SortedSetRemoveByRankFunc != nil {
		return s.SortedSetRemoveByRankFunc(ctx, key, start, stop)
	}
	return 0, notConfigured(s.name, "SortedSetRemoveByRank")
}

func (s *CacheStub) SortedSetRemoveByScore(ctx context.Context, key string, scores cache.ScoreRange) (int64, error) {
	s.record("SortedSetRemoveByScore")
	if s.SortedSetRemoveByScoreFunc != nil {
		return s.SortedSetRemoveByScoreFunc(ctx, key, scores)
	}
	return 0, notConfigured(s.name, "SortedSetRemoveByScore")
}

func (s *CacheStub) SortedSetScore(ctx context.Context, key string, member string) (float64, error) {
	s.record("SortedSetScore")
	if s.SortedSetScoreFunc != nil {
		return s.SortedSetScoreFunc(ctx, key, member)
	}
	return 0, notConfigured(s.name, "SortedSetScore")
}

func (s *CacheStub) TTL(ctx context.Context, key string) (time.Duration, bool, error) {
	s.record("TTL")
	if s.TTLFunc != nil {
		return s.TTLFunc(ctx, key)
	}
	return 0, false, notConfigured(s.name, "TTL")
}

func (s *CacheStub) Transaction(ctx context.Context, queue func(cache.Tx)) ([]any, error) {
	s.record("Transaction")
	if s.TransactionFunc != nil {
		return s.TransactionFunc(ctx, queue)
	}
	return nil, notConfigured(s.name, "Transaction")
}

func (s *CacheStub) Type(ctx context.Context, key string) (cache.KeyType, error) {
	s.record("Type")
	if s.TypeFunc != nil {
		return s.TypeFunc(ctx, key)
	}
	return "", notConfigured(s.name, "Type")
}
