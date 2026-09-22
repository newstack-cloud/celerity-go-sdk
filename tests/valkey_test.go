//go:build integration

package tests

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// The suite publishes to the streams the runtime's local consumer reads where a
// queue is celerity:queue:{sourceId} and a schedule is
// celerity:schedules:{scheduleId}, each entry carrying a body and the time it
// was produced.

var (
	clientOnce sync.Once
	client     *redis.Client
)

func valkey() *redis.Client {
	clientOnce.Do(func() {
		client = redis.NewClient(&redis.Options{Addr: valkeyAddr()})
	})
	return client
}

func valkeyAddr() string {
	if addr := os.Getenv("CELERITY_TEST_VALKEY_ADDR"); addr != "" {
		return addr
	}
	port := os.Getenv("CELERITY_TEST_VALKEY_PORT")
	if port == "" {
		port = "6379"
	}
	return "127.0.0.1:" + port
}

// Appends one entry to a stream, in the fields the consumer reads.
func publish(stream, body string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, err := valkey().XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"body":      body,
			"timestamp": strconv.FormatInt(time.Now().Unix(), 10),
		},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("publishing to %s at %s: %w", stream, valkeyAddr(), err)
	}
	return id, nil
}
