package datastore

import "errors"

// ErrInvalidCondition is a condition the store cannot check, which is what a
// [Condition] or a [SortCondition] built as a struct literal rather than by one
// of the functions here produces.
//
// An application error rather than a runtime condition, so it is reported before
// a request is made:
//
//	if errors.Is(err, datastore.ErrInvalidCondition) { ... }
//
// Sibling of [ErrInvalidUpdate], which is the same mistake made with an update.
var ErrInvalidCondition = errors.New("celerity: invalid condition")

// Operator is a test that a condition applies.
type Operator string

const (
	OpEqual          Operator = "eq"
	OpNotEqual       Operator = "ne"
	OpLess           Operator = "lt"
	OpLessOrEqual    Operator = "le"
	OpGreater        Operator = "gt"
	OpGreaterOrEqual Operator = "ge"
	OpBetween        Operator = "between"
	OpStartsWith     Operator = "startsWith"
	OpContains       Operator = "contains"
	OpExists         Operator = "exists"
	// OpNotExists is what an insert that must not overwrite is written as, so
	// it is here rather than left to each provider.
	OpNotExists Operator = "notExists"

	// The two that combine conditions rather than test an attribute.
	OpAnd Operator = "and"
	OpOr  Operator = "or"
)

// Condition is a test on an item, for a write that should only happen if the
// item is in a particular state as well as queries.
//
// Built with the functions below rather than by hand. The fields are exported
// because a provider module has to read one to translate it, not because a
// caller should be constructing them by hand, an example of using the functions would be:
//
//	resources.All(
//	    resources.Eq("status", "open"),
//	    resources.Gt("version", 3),
//	)
//
// Only operators every one of the supported backing stores can do are here.
// A store's own are reached through its provider package,
// which is where something that does not port belongs.
//
// # What a condition costs
//
// The meaning of these ports everywhere. What they cost does not, and it is
// worth knowing which is which before a hot path depends on one.
//
// [Exists] and [NotExists] are a precondition every one of the backing stores takes as
// part of the write itself: attribute_exists on DynamoDB, an exists
// precondition on Firestore, create rather than upsert on Cosmos DB. One
// request, everywhere.
//
// Testing an attribute's value is one request on DynamoDB, which evaluates the
// condition server-side. Firestore has no such precondition, only exists and a
// last-written time, so a provider there has to read the document inside a
// transaction and decide; Cosmos DB has no condition on properties either, and
// gets there by reading the item for its ETag and replacing with if-match.
// Both are correct and both cost a read the AWS provider does not.
//
// So an insert that must not overwrite is free to write portably. An optimistic
// update is not, and a store keeping a version attribute for the purpose is
// worth it either way, since it is what the extra read compares.
type Condition struct {
	// Op is the test. Always set; a zero Condition is refused by the provider
	// rather than quietly matching everything.
	Op Operator
	// Name is the attribute tested, and is empty for [All] and [Any].
	Name string
	// Value is what the attribute is tested against, and is unset for
	// [Exists], [NotExists], [All] and [Any].
	Value any
	// High is the upper bound of [Between], whose lower bound is Value.
	High any
	// Of are the conditions [All] and [Any] combine, and nest as deeply as
	// they are written.
	Of []Condition
}

// Eq matches an attribute equal to a value.
func Eq(name string, value any) Condition {
	return Condition{Op: OpEqual, Name: name, Value: value}
}

// Ne matches an attribute not equal to a value.
func Ne(name string, value any) Condition {
	return Condition{Op: OpNotEqual, Name: name, Value: value}
}

// Lt matches an attribute below a value.
func Lt(name string, value any) Condition {
	return Condition{Op: OpLess, Name: name, Value: value}
}

// Le matches an attribute at or below a value.
func Le(name string, value any) Condition {
	return Condition{Op: OpLessOrEqual, Name: name, Value: value}
}

// Gt matches an attribute above a value.
func Gt(name string, value any) Condition {
	return Condition{Op: OpGreater, Name: name, Value: value}
}

// Ge matches an attribute at or above a value.
func Ge(name string, value any) Condition {
	return Condition{Op: OpGreaterOrEqual, Name: name, Value: value}
}

// Between matches an attribute within a range, ends included.
func Between(name string, low, high any) Condition {
	return Condition{Op: OpBetween, Name: name, Value: low, High: high}
}

// StartsWith matches a string attribute beginning with a prefix.
func StartsWith(name, prefix string) Condition {
	return Condition{Op: OpStartsWith, Name: name, Value: prefix}
}

// Contains matches a string attribute holding a substring, or a set holding a
// member.
func Contains(name string, value any) Condition {
	return Condition{Op: OpContains, Name: name, Value: value}
}

// Exists matches an item that has the attribute at all.
func Exists(name string) Condition {
	return Condition{Op: OpExists, Name: name}
}

// NotExists matches an item that does not have the attribute.
//
// What an insert that must not overwrite is written as, on the store's own key:
//
//	err := orders.Put(ctx, key, order, resources.If(resources.NotExists("orderId")))
//
// The write is refused with [ErrConditionFailed] where the item is already
// there, which is the difference between creating and replacing.
func NotExists(name string) Condition {
	return Condition{Op: OpNotExists, Name: name}
}

// All matches where every condition matches.
func All(conditions ...Condition) Condition {
	return Condition{Op: OpAnd, Of: conditions}
}

// Any matches where at least one condition matches.
func Any(conditions ...Condition) Condition {
	return Condition{Op: OpOr, Of: conditions}
}

// SortCondition narrows a query to part of a partition.
//
// Every one of the backing stores can do all of these: DynamoDB in its key condition,
// Cosmos DB in a where clause with its own starts-with, Firestore in range
// filters, where a prefix is the range between it and its own upper bound. So a
// query that reads part of a partition reads part of one everywhere, and this is
// the cheaper half of the two condition types.
//
// Separate from [Condition] and built by the Sort functions because it doesn't carry
// attribute names: the attribute is the store's own sort key, which the store
// knows and a handler should not have to repeat. It is also why the operators
// are fewer, since a sort key is ordered and a test that is not about order
// cannot use that order to read less.
//
// Without one, a query reads a whole partition. With one it reads the part that
// was asked for, which is the difference between a cost that grows with the
// partition and one that grows with the answer.
type SortCondition struct {
	// Op is the test, and is one of equal, the four comparisons, between and
	// starts-with.
	Op Operator
	// Value is what the sort key is tested against, and the lower bound of
	// [SortBetween].
	Value string
	// High is the upper bound of [SortBetween].
	High string
}

// SortEqual matches one sort value, which addresses part of a composite key
// without naming the item.
func SortEqual(value string) *SortCondition {
	return &SortCondition{Op: OpEqual, Value: value}
}

// SortLess matches sort values below a value.
func SortLess(value string) *SortCondition {
	return &SortCondition{Op: OpLess, Value: value}
}

// SortLessOrEqual matches sort values at or below a value.
func SortLessOrEqual(value string) *SortCondition {
	return &SortCondition{Op: OpLessOrEqual, Value: value}
}

// SortGreater matches sort values above a value.
func SortGreater(value string) *SortCondition {
	return &SortCondition{Op: OpGreater, Value: value}
}

// SortGreaterOrEqual matches sort values at or above a value.
func SortGreaterOrEqual(value string) *SortCondition {
	return &SortCondition{Op: OpGreaterOrEqual, Value: value}
}

// SortBetween matches sort values within a range, ends included.
func SortBetween(low, high string) *SortCondition {
	return &SortCondition{Op: OpBetween, Value: low, High: high}
}

// SortStartsWith matches sort values beginning with a prefix, which is how a
// hierarchy held in a sort key is read one level at a time.
func SortStartsWith(prefix string) *SortCondition {
	return &SortCondition{Op: OpStartsWith, Value: prefix}
}

// WriteOption configures a write.
type WriteOption func(*WriteOptions)

// WriteOptions is the resolved configuration for a write.
type WriteOptions struct {
	// Condition that has to be true of the item already there, or the write is refused.
	Condition *Condition
	// Revision is the revision the item has to still carry, or the write is
	// refused. Nil when the write is not conditional on one.
	Revision *Revision
}

// If refuses a write unless the item is in the state described.
//
// This is what makes a read-then-write safe. Two invocations of a handler run
// at the same time, and one that reads an item, decides, and writes has no way
// to know the item did not change in between. A condition on the value it read
// turns the lost update into [ErrConditionFailed], which the handler can retry.
//
//	err := orders.Put(ctx, key, updated, resources.If(resources.Eq("version", was)))
func If(condition Condition) WriteOption {
	return func(o *WriteOptions) { o.Condition = &condition }
}

// IfUnchanged refuses a write unless the item still carries the revision a read
// returned, which is the portable way to do read-modify-write: one request per
// step on every store, where a condition on the item's own fields costs an
// extra read on some of them.
//
//	var order Order
//	rev, err := orders.Get(ctx, key, &order)
//	if err != nil {
//	    return err
//	}
//	order.Status = "paid"
//	if _, err := orders.Put(ctx, key, order, resources.IfUnchanged(rev)); err != nil {
//	    if errors.Is(err, resources.ErrConditionFailed) {
//	        return retry // something got there first
//	    }
//	    return err
//	}
//
// A revision that did not come from a read is refused with
// [ErrInvalidRevision] before a request is made, rather than quietly writing
// without a precondition.
//
// What this detects depends on the store. Where the store maintains the
// revision itself, it catches every writer. Where a provider maintains it in
// [RevisionField], it catches only writers that go through a Celerity SDK, so a
// write made with a provider's own SDK is invisible to it.
func IfUnchanged(revision Revision) WriteOption {
	return func(o *WriteOptions) { o.Revision = &revision }
}

// ResolveWriteOptions applies write options, for a provider implementing a
// write.
func ResolveWriteOptions(opts []WriteOption) WriteOptions {
	var options WriteOptions
	for _, opt := range opts {
		opt(&options)
	}
	return options
}
