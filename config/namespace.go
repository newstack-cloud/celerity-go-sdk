package config

import (
	"context"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/newstack-cloud/celerity-go-sdk/telemetry"
)

// Namespace contains the values of one store.
//
// An application with one celerity/config resource has one of these and reaches
// it through the [Service] directly. One with several names the one it means.
type Namespace interface {
	// Lookup returns a value and whether it is there.
	Lookup(ctx context.Context, key string) (string, bool, error)
	// Get returns a value, and a [MissingKeyError] where there is none.
	//
	// The two exist for the reason os.Getenv and os.LookupEnv do: a value the
	// application cannot run without should fail loudly at the point it is
	// read, and an optional one should not need an error branch.
	Get(ctx context.Context, key string) (string, error)
	// All returns every value the store holds.
	All(ctx context.Context) (map[string]string, error)
	// Bind fills a struct from the store, by `config` tag.
	Bind(ctx context.Context, out any) error
}

// NamespaceOption configures a namespace.
type NamespaceOption func(*namespace)

// WithPrefix serves only the keys under a prefix, with the prefix removed.
//
// A store shared between several applications, which a parameter store commonly
// is, keeps each one's values under its own path. The handler asks for
// "timeout" rather than "orders/timeout".
func WithPrefix(prefix string) NamespaceOption {
	return func(n *namespace) { n.prefix = prefix }
}

// WithRefresh re-reads the store once values are older than interval.
//
// Without it a store is read once and held for the life of the process, which
// for a serverless function means for the life of the execution environment: a
// rotated secret would not be picked up until the environment was recycled.
//
// The read that finds them stale is the one that waits for the re-read, so a
// caller is never handed values that have already been judged too old. A failed
// re-read is logged and the previous values stand, since values that are stale
// are worth more than no values at all, and it is not retried until the
// interval is up again.
func WithRefresh(interval time.Duration) NamespaceOption {
	return func(n *namespace) { n.refresh = interval }
}

// NewNamespace returns a namespace reading one store through a backend.
//
// The store is not read here. It is read on the first value asked for, so that
// an application declaring config it never reads pays nothing, and a cold start
// is not spent on a store this invocation does not touch.
func NewNamespace(backend Backend, storeID string, opts ...NamespaceOption) Namespace {
	n := &namespace{backend: backend, storeID: storeID}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

type namespace struct {
	backend Backend
	storeID string
	prefix  string
	refresh time.Duration

	mu     sync.Mutex
	values map[string]string
	// checkedAt is when the store was last read, successfully or not.
	checkedAt time.Time
}

func (n *namespace) Lookup(ctx context.Context, key string) (string, bool, error) {
	values, err := n.load(ctx)
	if err != nil {
		return "", false, err
	}

	value, ok := values[key]
	return value, ok, nil
}

func (n *namespace) Get(ctx context.Context, key string) (string, error) {
	value, ok, err := n.Lookup(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", &MissingKeyError{Namespace: n.storeID, Key: key}
	}
	return value, nil
}

func (n *namespace) All(ctx context.Context) (map[string]string, error) {
	values, err := n.load(ctx)
	if err != nil {
		return nil, err
	}

	// Copied, so that a caller cannot change what the next read sees.
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out, nil
}

func (n *namespace) Bind(ctx context.Context, out any) error {
	values, err := n.load(ctx)
	if err != nil {
		return err
	}
	return bindValues(values, out)
}

// load returns the store's values, reading the store when it has none or when
// what it has has gone stale.
//
// The read is synchronous, and the caller that crosses the staleness boundary
// waits for it. Refreshing behind that caller instead would hand it the stale
// values it had just decided were too old, and leave the fetch to finish
// whenever: a serverless execution environment is frozen once a handler
// returns, so work started on its way out resumes inside some later invocation
// or not at all, which is a poor way to bound how old a value can be.
//
// One reader pays for one fetch per interval, and the lock means concurrent
// readers wait on that one rather than each making their own.
func (n *namespace) load(ctx context.Context) (map[string]string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.values != nil && !n.stale() {
		return n.values, nil
	}

	values, err := n.backend.Fetch(ctx, n.storeID)

	// Recorded whether or not the read worked, so a store that is refusing
	// requests is retried once an interval rather than on every read.
	n.checkedAt = time.Now()

	if err != nil {
		if n.values == nil {
			return nil, err
		}
		// Values that are stale are worth more than none, and a store that is
		// briefly unreachable should not take an application down with it.
		telemetry.LoggerFrom(ctx).WarnContext(ctx,
			"could not re-read configuration, serving the values already held",
			"store", n.storeID,
			"error", err,
		)
		return n.values, nil
	}

	n.values = n.applyPrefix(values)
	return n.values, nil
}

// stale reports that the values are older than the deployment asked them to be.
//
// A refresh interval of zero holds them for the life of the process, which is
// what a backend that caches for itself gets.
func (n *namespace) stale() bool {
	return n.refresh > 0 && time.Since(n.checkedAt) >= n.refresh
}

func (n *namespace) applyPrefix(values map[string]string) map[string]string {
	if n.prefix == "" {
		return values
	}

	prefix := n.prefix + "/"
	out := make(map[string]string, len(values))
	for key, value := range values {
		if after, found := strings.CutPrefix(key, prefix); found {
			out[after] = value
		}
	}
	return out
}
