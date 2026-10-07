package celeritytest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// Datastore is a document store held in memory.
//
// Items are kept as the JSON they marshal to, which is how a real store keeps
// them as far as a handler is concerned: what goes in comes back out through
// the same encoding, so a field a type does not declare is not invented here
// and one it declares badly fails here too.
type Datastore struct {
	ref resources.Ref

	mu sync.Mutex
	// Keyed by partition then sort, so a query reads one partition without
	// walking the store.
	items map[string]map[string]storedItem
	// Counted so that a revision is stable across runs.
	revisions int
}

type storedItem struct {
	// encoded is the item as it was written.
	encoded []byte
	// fields is the same thing, for conditions and filters to read.
	fields   map[string]any
	revision string
}

// NewDatastore returns an empty data store.
func NewDatastore(name string) *Datastore {
	return &Datastore{
		ref:   resources.Ref{Kind: resources.KindDatastore, Name: name},
		items: map[string]map[string]storedItem{},
	}
}

// Len reports how many items the store holds.
func (d *Datastore) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	count := 0
	for _, partition := range d.items {
		count += len(partition)
	}
	return count
}

// Seed puts an item in without going through a handler, for arranging the
// state a test starts from.
func (d *Datastore) Seed(key datastore.Key, item any) error {
	_, err := d.Put(context.Background(), key, item)
	return err
}

func (d *Datastore) Get(
	ctx context.Context, key datastore.Key, out any,
) (datastore.Revision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	item, ok := d.items[key.Partition][key.Sort]
	if !ok {
		return datastore.Revision{}, d.absent(key)
	}

	if err := json.Unmarshal(item.encoded, out); err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: reading %s %v as %T: %w", d.ref, key, out, err)
	}

	return datastore.NewRevision(item.revision), nil
}

func (d *Datastore) Put(
	ctx context.Context, key datastore.Key, item any, opts ...datastore.WriteOption,
) (datastore.Revision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.put(key, item, datastore.ResolveWriteOptions(opts))
}

func (d *Datastore) put(
	key datastore.Key, item any, options datastore.WriteOptions,
) (datastore.Revision, error) {
	existing, held := d.items[key.Partition][key.Sort]
	if err := d.check(key, existing, held, options); err != nil {
		return datastore.Revision{}, err
	}

	encoded, err := json.Marshal(item)
	if err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: writing %s %v: %w", d.ref, key, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return datastore.Revision{}, fmt.Errorf(
			"celerity: %s holds objects, and %v was written as %s", d.ref, key, encoded)
	}

	d.revisions++
	revision := fmt.Sprintf("r%d", d.revisions)
	d.store(key, storedItem{encoded: encoded, fields: fields, revision: revision})
	return datastore.NewRevision(revision), nil
}

func (d *Datastore) store(key datastore.Key, item storedItem) {
	if d.items[key.Partition] == nil {
		d.items[key.Partition] = map[string]storedItem{}
	}
	d.items[key.Partition][key.Sort] = item
}

// check applies the preconditions a write asked for.
func (d *Datastore) check(
	key datastore.Key, existing storedItem, held bool, options datastore.WriteOptions,
) error {
	if options.Revision != nil {
		if !options.Revision.Known() {
			return datastore.ErrInvalidRevision
		}
		want, _ := options.Revision.Value()
		if !held || existing.revision != want {
			return d.refused(key)
		}
	}
	if options.Condition == nil {
		return nil
	}

	fields := existing.fields
	if !held {
		fields = nil
	}
	met, err := matches(*options.Condition, fields)
	if err != nil {
		return err
	}
	if !met {
		return d.refused(key)
	}
	return nil
}

func (d *Datastore) Delete(
	ctx context.Context, key datastore.Key, opts ...datastore.WriteOption,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.remove(key, datastore.ResolveWriteOptions(opts))
}

// remove deletes an item, and deleting one that is not there is not a failure.
func (d *Datastore) remove(key datastore.Key, options datastore.WriteOptions) error {
	existing, held := d.items[key.Partition][key.Sort]
	if err := d.check(key, existing, held, options); err != nil {
		return err
	}
	delete(d.items[key.Partition], key.Sort)
	return nil
}

func (d *Datastore) Query(
	ctx context.Context, q datastore.Query, out any,
) (datastore.Cursor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	sorts := make([]string, 0, len(d.items[q.Partition]))
	for sortKey := range d.items[q.Partition] {
		sorts = append(sorts, sortKey)
	}
	sort.Strings(sorts)
	if q.Descending {
		reverse(sorts)
	}

	var matched []storedItem
	for _, sortKey := range sorts {
		if q.Sort != nil && !sortMatches(*q.Sort, sortKey) {
			continue
		}
		item := d.items[q.Partition][sortKey]
		keep, err := filtered(q.Filter, item.fields)
		if err != nil {
			return "", err
		}
		if keep {
			matched = append(matched, item)
		}
	}

	return d.page(matched, q.Limit, q.Cursor, q.Project, out)
}

func (d *Datastore) Scan(
	ctx context.Context, s datastore.Scan, out any,
) (datastore.Cursor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	partitions := make([]string, 0, len(d.items))
	for partition := range d.items {
		partitions = append(partitions, partition)
	}
	sort.Strings(partitions)

	var matched []storedItem
	for _, partition := range partitions {
		sorts := make([]string, 0, len(d.items[partition]))
		for sortKey := range d.items[partition] {
			sorts = append(sorts, sortKey)
		}
		sort.Strings(sorts)

		for _, sortKey := range sorts {
			item := d.items[partition][sortKey]
			keep, err := filtered(s.Filter, item.fields)
			if err != nil {
				return "", err
			}
			if keep {
				matched = append(matched, item)
			}
		}
	}

	return d.page(matched, s.Limit, s.Cursor, s.Project, out)
}

// page fills out with one page of items and answers where to resume.
func (d *Datastore) page(
	matched []storedItem, limit int, cursor string, project []string, out any,
) (datastore.Cursor, error) {
	start := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &start); err != nil {
			return "", datastore.ErrInvalidCursor
		}
	}
	if start > len(matched) {
		return "", datastore.ErrInvalidCursor
	}

	end := len(matched)
	if limit > 0 {
		end = min(start+limit, end)
	}

	page := make([]json.RawMessage, 0, end-start)
	revisions := make([]datastore.Revision, 0, end-start)
	for _, item := range matched[start:end] {
		encoded, err := projected(item, project)
		if err != nil {
			return "", err
		}
		page = append(page, encoded)
		revisions = append(revisions, datastore.NewRevision(item.revision))
	}

	if err := fill(out, page); err != nil {
		return "", err
	}
	datastore.DeliverRevisions(out, revisions)

	if end < len(matched) {
		return datastore.Cursor(fmt.Sprintf("%d", end)), nil
	}
	return "", nil
}

// projected narrows an item to the fields a read asked for.
func projected(item storedItem, project []string) (json.RawMessage, error) {
	if len(project) == 0 {
		return item.encoded, nil
	}

	narrowed := map[string]any{}
	for _, name := range project {
		if value, ok := item.fields[name]; ok {
			narrowed[name] = value
		}
	}
	return json.Marshal(narrowed)
}

// fill decodes a page into the destination a caller gave, which is a pointer to
// a slice of their own type.
func fill(out any, page []json.RawMessage) error {
	encoded, err := json.Marshal(page)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("celerity: reading a page as %T: %w", out, err)
	}
	return nil
}

func (d *Datastore) BatchGet(
	ctx context.Context, keys []datastore.Key, out any,
) ([]datastore.Key, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var found []json.RawMessage
	var revisions []datastore.Revision
	var missing []datastore.Key
	for _, key := range keys {
		item, ok := d.items[key.Partition][key.Sort]
		if !ok {
			// A key that holds nothing is absent rather than an error, so a
			// caller matches items to keys by key and never by position.
			missing = append(missing, key)
			continue
		}
		found = append(found, item.encoded)
		revisions = append(revisions, datastore.NewRevision(item.revision))
	}

	if err := fill(out, found); err != nil {
		return nil, err
	}
	datastore.DeliverRevisions(out, revisions)
	return missing, nil
}

func (d *Datastore) Update(
	ctx context.Context, key datastore.Key, updates []datastore.Update,
	opts ...datastore.WriteOption,
) (datastore.Revision, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.update(key, updates, datastore.ResolveWriteOptions(opts))
}

func (d *Datastore) update(
	key datastore.Key, updates []datastore.Update, options datastore.WriteOptions,
) (datastore.Revision, error) {
	if len(updates) > datastore.MaxUpdates {
		return datastore.Revision{}, datastore.ErrTooManyOperations
	}

	existing, held := d.items[key.Partition][key.Sort]
	if !held {
		// An update of a key that holds nothing reports absence rather than
		// creating it, which is what keeps the same code from making a
		// half-populated item on one store and failing on another.
		return datastore.Revision{}, d.absent(key)
	}
	if err := d.check(key, existing, held, options); err != nil {
		return datastore.Revision{}, err
	}

	fields := map[string]any{}
	for name, value := range existing.fields {
		fields[name] = value
	}
	for _, change := range updates {
		if err := apply(change, fields); err != nil {
			return datastore.Revision{}, err
		}
	}

	encoded, err := json.Marshal(fields)
	if err != nil {
		return datastore.Revision{}, err
	}

	d.revisions++
	revision := fmt.Sprintf("r%d", d.revisions)
	d.store(key, storedItem{encoded: encoded, fields: fields, revision: revision})
	return datastore.NewRevision(revision), nil
}

// apply makes one mutation to an item's fields.
func apply(change datastore.Update, fields map[string]any) error {
	if change.Path == "" {
		return datastore.ErrInvalidUpdate
	}
	// Object fields only, as the contract states, so a dotted path walks
	// objects and nothing else.
	parent, leaf, err := walk(fields, change.Path)
	if err != nil {
		return err
	}

	switch change.Kind {
	case datastore.UpdateSet:
		parent[leaf] = change.Value
	case datastore.UpdateRemove:
		delete(parent, leaf)
	case datastore.UpdateIncrement:
		current, _ := parent[leaf].(float64)
		parent[leaf] = current + change.By
	default:
		return datastore.ErrInvalidUpdate
	}
	return nil
}

// walk follows a dotted path to the object holding the field it names,
// creating the objects along the way the way a set does.
func walk(fields map[string]any, path string) (map[string]any, string, error) {
	segments := strings.Split(path, ".")
	parent := fields
	for _, segment := range segments[:len(segments)-1] {
		next, ok := parent[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			parent[segment] = next
		}
		parent = next
	}
	return parent, segments[len(segments)-1], nil
}

func (d *Datastore) BatchWrite(
	ctx context.Context, ops []datastore.BatchOp,
) ([]datastore.BatchOp, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Not atomic, as the contract says: each is applied on its own and what
	// could not be is handed back.
	var unapplied []datastore.BatchOp
	for _, op := range ops {
		var err error
		if op.IsDelete() {
			err = d.remove(op.Key, datastore.WriteOptions{})
		} else {
			_, err = d.put(op.Key, op.Item, datastore.WriteOptions{})
		}
		if err != nil {
			unapplied = append(unapplied, op)
		}
	}
	return unapplied, nil
}

func (d *Datastore) Atomically(
	ctx context.Context, partition string, ops []datastore.AtomicOp,
) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(ops) > datastore.MaxAtomicOps {
		return datastore.ErrTooManyOperations
	}
	for _, op := range ops {
		if op.Key.Partition != partition {
			return datastore.ErrWrongPartition
		}
	}

	// Every operation or none, so the store is copied, the batch applied to
	// the original, and the snapshot restored if there were any failures.
	restore := d.snapshot()
	for _, op := range ops {
		if err := d.applyAtomic(op); err != nil {
			d.items = restore
			return err
		}
	}
	return nil
}

func (d *Datastore) applyAtomic(op datastore.AtomicOp) error {
	switch {
	case op.IsPut():
		_, err := d.put(op.Key, op.Item, op.Options)
		return err
	case op.IsUpdate():
		_, err := d.update(op.Key, op.Updates, op.Options)
		return err
	default:
		return d.remove(op.Key, op.Options)
	}
}

func (d *Datastore) snapshot() map[string]map[string]storedItem {
	copied := make(map[string]map[string]storedItem, len(d.items))
	for partition, items := range d.items {
		copied[partition] = make(map[string]storedItem, len(items))
		for sortKey, item := range items {
			copied[partition][sortKey] = item
		}
	}
	return copied
}

func (d *Datastore) absent(key datastore.Key) error {
	return fmt.Errorf(
		"celerity: %s holds no item %v: %w", d.ref, key, datastore.ErrNotFound,
	)
}

func (d *Datastore) refused(key datastore.Key) error {
	return fmt.Errorf("celerity: %s refused a write to %v: %w",
		d.ref, key, datastore.ErrConditionFailed)
}

func reverse(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
