//go:build integration

// The Redis backend for a session's queue and topic, against a real Valkey,
// which is what a `celerity dev` session runs.
//
// Driven through the provider this package registers into rather than through
// its own types, which is how a handler reaches it.
//
// What a unit test mock cannot establish: that what a send writes is what the
// runtime's consumer reads. The stream key, the field names and the message
// type are a contract with another program, the same way the handler tags are,
// so the assertions here read the stream back the way that program does rather
// than the way this package wrote it.
//
// Run with: bash scripts/run-tests.sh --with-integration
package redis_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/config"
	"github.com/newstack-cloud/celerity-go-sdk/resources"
	"github.com/newstack-cloud/celerity-go-sdk/resources/local"

	// Named rather than blank: importing it is what registers the backend, and
	// the suite also reads the variable it takes its endpoint from.
	localredis "github.com/newstack-cloud/celerity-go-sdk/resources/local/redis"
	"github.com/newstack-cloud/celerity-go-sdk/resources/queue"
	"github.com/newstack-cloud/celerity-go-sdk/resources/topic"
)

type LocalIntegrationTestSuite struct {
	suite.Suite

	client   goredis.UniversalClient
	provider *local.Provider
	// run keeps this run's streams and channels apart from an earlier one's,
	// since a stream is not torn down between runs.
	run string
}

func TestLocalIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(LocalIntegrationTestSuite))
}

func (s *LocalIntegrationTestSuite) SetupSuite() {
	endpoint := "redis://127.0.0.1:" + portOr("CELERITY_TEST_VALKEY_PORT", "6379")
	s.T().Setenv(localredis.EndpointEnvVar, endpoint)

	options, err := goredis.ParseURL(endpoint)
	s.Require().NoError(err)
	s.client = goredis.NewClient(options)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.client.Ping(ctx).Err(); err != nil {
		s.T().Skipf("Valkey is not reachable at %s: %v", endpoint, err)
	}

	s.run = strconv.FormatInt(time.Now().UnixNano(), 36)
	s.provider = local.New()
}

func (s *LocalIntegrationTestSuite) TearDownSuite() {
	if s.client != nil {
		s.Require().NoError(s.client.Close())
	}
	if s.provider != nil {
		s.Require().NoError(s.provider.Close(context.Background()))
	}
}

func (s *LocalIntegrationTestSuite) Test_a_send_writes_the_entry_the_runtime_reads() {
	// The fields the runtime's consumer requires are body and timestamp: it
	// gives up without them. The rest it reads when they are there.
	ctx := context.Background()
	name := "orders-" + s.run
	sender := s.queue(name)

	id, err := sender.Send(ctx, []byte(`{"id":1}`),
		func(o *queue.SendOptions) { o.GroupID = "orders" },
		func(o *queue.SendOptions) { o.DeduplicationID = "order-1" },
		func(o *queue.SendOptions) { o.Attributes = map[string]string{"kind": "created"} },
	)
	s.Require().NoError(err)
	s.NotEmpty(id)

	fields := s.readOnly(ctx, "celerity:queue:"+name)
	s.Equal(`{"id":1}`, fields["body"])
	s.Equal("0", fields["message_type"], "a UTF-8 body is text")
	s.Equal("orders", fields["group_id"])
	s.Equal("order-1", fields["dedup_id"])
	s.JSONEq(`{"kind":"created"}`, fields["attributes"].(string))

	timestamp, err := strconv.ParseInt(fields["timestamp"].(string), 10, 64)
	s.Require().NoError(err, "the consumer parses this as an integer and gives up if it cannot")
	s.WithinDuration(time.Now(), time.Unix(timestamp, 0), time.Minute)
}

func (s *LocalIntegrationTestSuite) Test_a_body_that_is_not_text_is_marked_as_binary() {
	// A queue takes bytes and a stream field is a string, which is why the
	// entry carries a type: the consumer decodes base64 when it says binary.
	ctx := context.Background()
	name := "binary-" + s.run
	body := []byte{0x00, 0xff, 0xfe, 0x01}

	_, err := s.queue(name).Send(ctx, body)
	s.Require().NoError(err)

	fields := s.readOnly(ctx, "celerity:queue:"+name)
	s.Equal("1", fields["message_type"])
	decoded, err := base64.StdEncoding.DecodeString(fields["body"].(string))
	s.Require().NoError(err)
	s.Equal(body, decoded)
}

func (s *LocalIntegrationTestSuite) Test_a_batch_is_one_round_trip_and_reports_each_entry() {
	ctx := context.Background()
	name := "batch-" + s.run

	result, err := s.queue(name).SendBatch(ctx, []queue.BatchEntry{
		{ID: "a", Body: []byte("one")},
		{ID: "b", Body: []byte("two")},
		{ID: "c", Body: []byte("three")},
	})

	s.Require().NoError(err)
	s.Require().Len(result.Successful, 3)
	s.Empty(result.Failed)
	s.Empty(result.Unsent)
	s.Equal([]string{"a", "b", "c"}, []string{
		result.Successful[0].ID, result.Successful[1].ID, result.Successful[2].ID,
	})

	length, err := s.client.XLen(ctx, "celerity:queue:"+name).Result()
	s.Require().NoError(err)
	s.EqualValues(3, length)
}

func (s *LocalIntegrationTestSuite) Test_a_publish_writes_the_envelope_the_bridge_parses() {
	// A topic goes to a channel, which the session's bridge subscribes to and
	// relays into a stream per consumer. It parses these field names, and falls
	// back to treating the whole payload as the body when it cannot, so a
	// mismatch would reach a handler as JSON it never sent.
	ctx := context.Background()
	name := "events-" + s.run
	channel := "celerity:topic:channel:" + name

	received := s.client.Subscribe(ctx, channel)
	defer received.Close()
	_, err := received.Receive(ctx)
	s.Require().NoError(err, "subscribing before publishing, since a channel keeps nothing")

	id, err := s.topic(name).Publish(ctx, []byte(`{"id":1}`),
		func(o *topic.SendOptions) { o.Subject = "An order was created" },
		func(o *topic.SendOptions) { o.Attributes = map[string]string{"kind": "created"} },
	)
	s.Require().NoError(err)
	s.NotEmpty(id)

	message, err := received.ReceiveMessage(ctx)
	s.Require().NoError(err)

	var envelope struct {
		Body       string            `json:"body"`
		MessageID  string            `json:"messageId"`
		Subject    string            `json:"subject"`
		Attributes map[string]string `json:"attributes"`
	}
	s.Require().NoError(json.Unmarshal([]byte(message.Payload), &envelope))
	s.Equal(`{"id":1}`, envelope.Body)
	s.Equal(id, envelope.MessageID, "the id a publish returns is the one the bridge relays")
	s.Equal("An order was created", envelope.Subject)
	s.Equal(map[string]string{"kind": "created"}, envelope.Attributes)
}

func (s *LocalIntegrationTestSuite) Test_a_publish_of_bytes_that_are_not_text_is_refused() {
	// The bridge marks everything it relays as text, so there is nowhere to say
	// a body is binary and delivering base64 would look like a message.
	ctx := context.Background()

	_, err := s.topic("binary-"+s.run).Publish(ctx, []byte{0x00, 0xff})

	s.Require().Error(err)
	s.Contains(err.Error(), "carries text")
}

func (s *LocalIntegrationTestSuite) Test_a_delayed_send_is_taken_without_the_delay() {
	// A delay is a real queue's and the session is standing in for it, so
	// refusing one would fail in a session the code a deployment runs.
	ctx := context.Background()
	name := "delayed-send-" + s.run

	_, err := s.queue(name).Send(ctx, []byte("body"),
		func(o *queue.SendOptions) { o.Delay = time.Minute })

	s.Require().NoError(err)
	fields := s.readOnly(ctx, "celerity:queue:"+name)
	s.Equal("body", fields["body"], "readable at once rather than held back")
}

func (s *LocalIntegrationTestSuite) queue(name string) queue.Client {
	client, err := s.provider.Queue(refNamed(resources.KindQueue, name))
	s.Require().NoError(err)
	return client
}

func (s *LocalIntegrationTestSuite) topic(name string) topic.Client {
	client, err := s.provider.Topic(refNamed(resources.KindTopic, name))
	s.Require().NoError(err)
	return client
}

// readOnly reads the single entry a stream holds, the way the runtime's
// consumer reads it: as a field map off a stream id.
func (s *LocalIntegrationTestSuite) readOnly(ctx context.Context, stream string) map[string]any {
	entries, err := s.client.XRange(ctx, stream, "-", "+").Result()
	s.Require().NoError(err)
	s.Require().Len(entries, 1, "one send should have written one entry")
	return entries[0].Values
}

// refNamed builds the pair a deployment writes: the links file saying which
// config key holds a resource, and the identifier recorded under it. A session
// records the blueprint name itself, since there is nothing deployed to have
// been given another.
func refNamed(kind resources.Kind, name string) resources.Ref {
	svc := config.New().WithLinks(config.Links{
		name: {Type: string(kind), ConfigKey: name},
	})
	svc.Register(config.ResourcesNamespace, config.NewNamespace(
		config.MapBackend{"resources": map[string]string{name: name}}, "resources"))
	return resources.Ref{Kind: kind, Name: name, Config: svc}
}

func portOr(name, fallback string) string {
	if port := os.Getenv(name); port != "" {
		return port
	}
	return fallback
}
