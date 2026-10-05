package redis

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	goredis "github.com/redis/go-redis/v9"

	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/local/internal/backend"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

// Linking this package is what gives a development session a queue and a topic.
//
// `celerity-go generate --local` writes the import. The provider itself knows
// nothing about Redis: it asks the seam for whatever was registered, which is
// what lets a different local backend replace this one without the provider
// changing.
func init() {
	connection := newConnection()
	backend.RegisterQueue(func(ref resources.Ref) (queue.Client, error) {
		return &redisQueue{conn: connection, ref: ref}, nil
	})
	backend.RegisterTopic(func(ref resources.Ref) (topic.Client, error) {
		return &redisTopic{conn: connection, ref: ref}, nil
	})
	backend.RegisterReleaser(func(context.Context) error {
		return connection.close()
	})
}

// EndpointEnvVar is where the CLI says which Redis a development session runs.
//
// The same variable the other SDKs read, since it is the session that
// sets it and one session serves handlers in any of the three.
const EndpointEnvVar = "CELERITY_REDIS_ENDPOINT"

// DefaultEndpoint is where a session runs Redis when nothing says otherwise.
const DefaultEndpoint = "redis://localhost:6379"

// connection is the one client a session's queues and topics share.
//
// One rather than one per resource: they are all the same Redis, and a client
// is a connection pool, so a handle per blueprint resource would be a pool per
// blueprint resource against a single-node Redis.
type connection struct {
	once   sync.Once
	client goredis.UniversalClient
	err    error

	// opened is the client once it has been built, for a shutdown to read
	// without consuming the Once.
	opened atomic.Pointer[goredis.UniversalClient]
}

func newConnection() *connection {
	return &connection{}
}

// get builds the client on the first call a handler makes.
//
// Nothing connects here: a Redis client dials lazily and keeps a pool, so this
// resolves the endpoint and hands back something that connects when it is first
// used.
func (c *connection) get() (goredis.UniversalClient, error) {
	c.once.Do(func() {
		endpoint := os.Getenv(EndpointEnvVar)
		if endpoint == "" {
			endpoint = DefaultEndpoint
		}

		options, err := goredis.ParseURL(endpoint)
		if err != nil {
			c.err = fmt.Errorf(
				"celerity: %s is not a Redis URL: %q: %w", EndpointEnvVar, endpoint, err)
			return
		}
		c.client = goredis.NewClient(options)
		c.opened.Store(&c.client)
	})
	return c.client, c.err
}

func (c *connection) close() error {
	client := c.opened.Load()
	if client == nil {
		return nil
	}
	if err := (*client).Close(); err != nil {
		return fmt.Errorf("celerity: closing the connection to the session's Redis: %w", err)
	}
	return nil
}
