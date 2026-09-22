package config_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
)

// A store is read over the network and charged for per request, so when it is
// read matters as much as what it returns.
type NamespaceTestSuite struct {
	suite.Suite
}

func TestNamespaceTestSuite(t *testing.T) {
	suite.Run(t, new(NamespaceTestSuite))
}

// countingBackend records what was asked of it and can be made to fail.
type countingBackend struct {
	mu      sync.Mutex
	values  map[string]string
	err     error
	fetches atomic.Int64
	// gate blocks a fetch until released, so a test can hold one open and
	// watch what concurrent readers do.
	gate chan struct{}
}

func (b *countingBackend) Fetch(context.Context, string) (map[string]string, error) {
	b.fetches.Add(1)
	if b.gate != nil {
		<-b.gate
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}

	out := make(map[string]string, len(b.values))
	maps.Copy(out, b.values)
	return out, nil
}

func (b *countingBackend) set(values map[string]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values = values
}

func (b *countingBackend) fail(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.err = err
}

func (s *NamespaceTestSuite) Test_the_store_is_not_read_until_a_value_is_asked_for() {
	backend := &countingBackend{values: map[string]string{"REGION": "eu-west-2"}}

	ns := config.NewNamespace(backend, "settings")

	// An application declaring config it never reads pays nothing, and a cold
	// start is not spent on a store this invocation does not touch.
	s.Equal(int64(0), backend.fetches.Load())

	value, err := ns.Get(context.Background(), "REGION")

	s.Require().NoError(err)
	s.Equal("eu-west-2", value)
	s.Equal(int64(1), backend.fetches.Load())
}

func (s *NamespaceTestSuite) Test_the_store_is_read_once_however_many_values_are_read() {
	backend := &countingBackend{values: map[string]string{"A": "1", "B": "2"}}
	ns := config.NewNamespace(backend, "settings")

	for range 5 {
		_, err := ns.Get(context.Background(), "A")
		s.Require().NoError(err)
	}
	_, err := ns.All(context.Background())
	s.Require().NoError(err)

	s.Equal(int64(1), backend.fetches.Load(), "the backend request should only be made once in a short period of time")
}

func (s *NamespaceTestSuite) Test_concurrent_first_readers_make_one_request() {
	// A cold start with several handlers reading config should make one
	// request, not one per reader.
	backend := &countingBackend{
		values: map[string]string{"A": "1"},
		gate:   make(chan struct{}),
	}
	ns := config.NewNamespace(backend, "settings")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, _ = ns.Get(context.Background(), "A")
		})
	}

	// Let them all arrive, then release the one fetch they are waiting behind.
	time.Sleep(50 * time.Millisecond)
	close(backend.gate)
	wg.Wait()

	s.Equal(int64(1), backend.fetches.Load())
}

func (s *NamespaceTestSuite) Test_a_missing_key_is_reported_rather_than_read_as_empty() {
	backend := &countingBackend{values: map[string]string{"A": "1"}}
	ns := config.NewNamespace(backend, "settings")

	_, err := ns.Get(context.Background(), "ABSENT")

	var missing *config.MissingKeyError
	s.Require().ErrorAs(err, &missing)
	s.Equal("ABSENT", missing.Key)
	s.Contains(err.Error(), "settings", "the error should name the store it looked in")
}

func (s *NamespaceTestSuite) Test_an_optional_value_needs_no_error_branch() {
	backend := &countingBackend{values: map[string]string{"A": "1"}}
	ns := config.NewNamespace(backend, "settings")

	value, ok, err := ns.Lookup(context.Background(), "ABSENT")

	s.Require().NoError(err)
	s.False(ok)
	s.Empty(value)
}

func (s *NamespaceTestSuite) Test_a_store_that_could_not_be_read_is_an_error() {
	backend := &countingBackend{err: errors.New("the parameter store refused the request")}
	ns := config.NewNamespace(backend, "settings")

	_, err := ns.Get(context.Background(), "A")

	s.Require().Error(err)
	s.Contains(err.Error(), "refused the request")
}

func (s *NamespaceTestSuite) Test_a_prefix_serves_one_applications_share_of_a_shared_store() {
	// A parameter store shared between applications keeps each one's values
	// under its own path, and the handler asks for the short name.
	backend := &countingBackend{values: map[string]string{
		"orders/TIMEOUT":  "30s",
		"orders/RETRIES":  "3",
		"billing/TIMEOUT": "90s",
	}}

	ns := config.NewNamespace(backend, "settings", config.WithPrefix("orders"))

	all, err := ns.All(context.Background())

	s.Require().NoError(err)
	s.Equal(map[string]string{"TIMEOUT": "30s", "RETRIES": "3"}, all)
}

func (s *NamespaceTestSuite) Test_values_are_held_until_they_go_stale() {
	backend := &countingBackend{values: map[string]string{"A": "first"}}
	ns := config.NewNamespace(backend, "settings", config.WithRefresh(50*time.Millisecond))

	value, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)
	s.Equal("first", value)

	// Not yet stale, so nothing is re-read.
	value, err = ns.Get(context.Background(), "A")
	s.Require().NoError(err)
	s.Equal("first", value)
	s.Equal(int64(1), backend.fetches.Load())
}

func (s *NamespaceTestSuite) Test_the_reader_that_finds_them_stale_waits_for_the_re_read() {
	// Rather than being handed the values it has just decided are too old,
	// with the re-read left to finish behind it.
	backend := &countingBackend{values: map[string]string{"A": "first"}}
	ns := config.NewNamespace(backend, "settings", config.WithRefresh(20*time.Millisecond))

	_, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)

	backend.set(map[string]string{"A": "second"})
	time.Sleep(30 * time.Millisecond)

	value, err := ns.Get(context.Background(), "A")

	s.Require().NoError(err)
	s.Equal("second", value, "the value was stale and the reader was told so anyway")
	s.Equal(int64(2), backend.fetches.Load())
}

func (s *NamespaceTestSuite) Test_a_failed_re_read_serves_the_values_already_held() {
	// Stale configuration is worth more than none, and a store that is briefly
	// unreachable should not take an application down with it.
	backend := &countingBackend{values: map[string]string{"A": "first"}}
	ns := config.NewNamespace(backend, "settings", config.WithRefresh(20*time.Millisecond))

	_, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)

	backend.fail(errors.New("the parameter store is unreachable"))
	time.Sleep(30 * time.Millisecond)

	value, err := ns.Get(context.Background(), "A")

	s.Require().NoError(err, "a failed re-read must not surface to a reader")
	s.Equal("first", value)
}

func (s *NamespaceTestSuite) Test_a_store_that_is_refusing_requests_is_not_retried_on_every_read() {
	backend := &countingBackend{values: map[string]string{"A": "first"}}
	ns := config.NewNamespace(backend, "settings", config.WithRefresh(50*time.Millisecond))

	_, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)

	backend.fail(errors.New("the parameter store is unreachable"))
	time.Sleep(60 * time.Millisecond)

	for range 5 {
		_, err := ns.Get(context.Background(), "A")
		s.Require().NoError(err)
	}

	// The first read after it went stale tried, and the rest were served from
	// what was held until the interval was up again.
	s.Equal(int64(2), backend.fetches.Load())
}

func (s *NamespaceTestSuite) Test_a_failing_store_is_tried_again_once_the_interval_is_up() {
	backend := &countingBackend{values: map[string]string{"A": "first"}}
	ns := config.NewNamespace(backend, "settings", config.WithRefresh(20*time.Millisecond))

	_, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)

	backend.fail(errors.New("unreachable"))
	time.Sleep(30 * time.Millisecond)
	_, err = ns.Get(context.Background(), "A")
	s.Require().NoError(err)

	// Recovered, and the next read once the interval is up picks it up.
	backend.fail(nil)
	backend.set(map[string]string{"A": "second"})
	time.Sleep(30 * time.Millisecond)

	value, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)
	s.Equal("second", value)
}

func (s *NamespaceTestSuite) Test_a_caller_cannot_change_what_the_next_read_sees() {
	backend := &countingBackend{values: map[string]string{"A": "1"}}
	ns := config.NewNamespace(backend, "settings")

	all, err := ns.All(context.Background())
	s.Require().NoError(err)
	all["A"] = "tampered"
	delete(all, "A")

	value, err := ns.Get(context.Background(), "A")
	s.Require().NoError(err)
	s.Equal("1", value)
}
