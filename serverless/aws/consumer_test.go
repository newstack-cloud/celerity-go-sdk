package aws_test

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/suite"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
	awsadapter "github.com/newstack-cloud/celerity-go-sdk/serverless/aws"
)

// Every Celerity consumer arrives over SQS, including one bound to a bucket or
// a datastore, so these cases cover both the plain message and the originating
// event a message can be carrying.
type ConsumerTestSuite struct {
	suite.Suite
	mapper awsadapter.Mapper
}

func TestConsumerTestSuite(t *testing.T) {
	suite.Run(t, new(ConsumerTestSuite))
}

const ordersTag = "source::ordersQueue::processOrders"

func (s *ConsumerTestSuite) Test_an_sqs_event_becomes_a_batch() {
	batch, err := s.mapper.ToConsumerBatch(fixture(&s.Suite, "sqs.json"), ordersTag)

	s.Require().NoError(err)
	s.Require().Len(batch.Records, 2)
	s.Equal("msg-1", batch.Records[0].MessageID)
	s.JSONEq(`{"orderId":"ord_42"}`, string(batch.Records[0].Body))
	s.Equal("arn:aws:sqs:eu-west-2:123456789012:orders-queue", batch.Records[0].Source)
	s.Equal("queue", batch.SourceType)
}

func (s *ConsumerTestSuite) Test_the_source_is_the_id_the_blueprint_gave_it() {
	cases := []struct {
		name string
		tag  string
		want string
	}{
		{
			// The same id the Celerity runtime reports, which the handler tag
			// already carries.
			name: "read from the handler tag",
			tag:  ordersTag,
			want: "ordersQueue",
		},
		{
			// True, but an AWS name rather than the blueprint's.
			name: "the queue itself, for a function whose tag says nothing",
			tag:  "",
			want: "orders-queue",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			batch, err := s.mapper.ToConsumerBatch(fixture(&s.Suite, "sqs.json"), tc.tag)

			s.Require().NoError(err)
			s.Equal(tc.want, batch.SourceID)
		})
	}
}

func (s *ConsumerTestSuite) Test_a_bucket_notification_is_unwrapped() {
	body := `{"Records":[{"eventSource":"aws:s3","eventName":"ObjectCreated:Put",` +
		`"s3":{"bucket":{"name":"reports"},"object":{"key":"q1/summary.pdf","size":2048,"eTag":"abc"}}}]}`

	batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(body), "source::reports::onUpload")

	s.Require().NoError(err)
	s.Require().Len(batch.Records, 1)
	// A handler bound to a bucket is given the object event, which is what it
	// receives under the Celerity runtime, rather than an SQS message that
	// happens to contain one.
	s.Equal("bucket", batch.SourceType)
	s.Equal("created", batch.Records[0].EventType)
	s.JSONEq(`{"key":"q1/summary.pdf","size":2048,"eTag":"abc"}`, string(batch.Records[0].Body))
}

func (s *ConsumerTestSuite) Test_bucket_event_names_map_to_the_shared_vocabulary() {
	cases := []struct {
		eventName string
		want      string
	}{
		{"ObjectCreated:Put", "created"},
		{"s3:ObjectCreated:CompleteMultipartUpload", "created"},
		{"ObjectRestore:Completed", "created"},
		{"ObjectRemoved:Delete", "deleted"},
		{"ObjectTagging:Put", "metadataUpdated"},
		{"ObjectAcl:Put", "metadataUpdated"},
		{"ReducedRedundancyLostObject", ""},
	}

	for _, tc := range cases {
		s.Run(tc.eventName, func() {
			body := `{"Records":[{"eventSource":"aws:s3","eventName":"` + tc.eventName + `",` +
				`"s3":{"bucket":{"name":"reports"},"object":{"key":"k"}}}]}`

			batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(body), "")

			s.Require().NoError(err)
			s.Equal(tc.want, batch.Records[0].EventType)
		})
	}
}

func (s *ConsumerTestSuite) Test_a_datastore_stream_record_is_unwrapped() {
	body := `{"eventSource":"aws:dynamodb","eventName":"MODIFY",` +
		`"eventSourceARN":"arn:aws:dynamodb:eu-west-2:1:table/orders/stream/2026-09-18T00:00:00.000",` +
		`"dynamodb":{"Keys":{"orderId":{"S":"ord_42"}},` +
		`"NewImage":{"orderId":{"S":"ord_42"},"total":{"N":"1999"},"paid":{"BOOL":true}},` +
		`"OldImage":{"orderId":{"S":"ord_42"},"total":{"N":"1499"}}}}`

	batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(body), "source::orders::onChange")

	s.Require().NoError(err)
	s.Require().Len(batch.Records, 1)
	s.Equal("datastore", batch.SourceType)
	s.Equal("modified", batch.Records[0].EventType)
	// The item, not DynamoDB's type tagging of it.
	s.JSONEq(
		`{"keys":{"orderId":"ord_42"},`+
			`"newItem":{"orderId":"ord_42","total":1999,"paid":true},`+
			`"oldItem":{"orderId":"ord_42","total":1499}}`,
		string(batch.Records[0].Body),
	)
}

func (s *ConsumerTestSuite) Test_datastore_event_names_map_to_the_shared_vocabulary() {
	cases := []struct {
		eventName string
		want      string
	}{
		{"INSERT", "inserted"},
		{"MODIFY", "modified"},
		{"REMOVE", "removed"},
		{"SOMETHING_NEW", ""},
	}

	for _, tc := range cases {
		s.Run(tc.eventName, func() {
			body := `{"eventSource":"aws:dynamodb","eventName":"` + tc.eventName +
				`","dynamodb":{"Keys":{"id":{"S":"1"}}}}`

			batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(body), "")

			s.Require().NoError(err)
			s.Equal(tc.want, batch.Records[0].EventType)
		})
	}
}

func (s *ConsumerTestSuite) Test_a_large_number_keeps_every_digit() {
	// DynamoDB carries up to 38 significant digits and a float64 holds about
	// 16, so rounding here would change an id or a currency amount into
	// something the caller never stored.
	body := `{"eventSource":"aws:dynamodb","eventName":"INSERT",` +
		`"dynamodb":{"Keys":{"id":{"N":"12345678901234567890123"}}}}`

	batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(body), "")

	s.Require().NoError(err)
	s.Contains(string(batch.Records[0].Body), "12345678901234567890123")
}

func (s *ConsumerTestSuite) Test_an_application_message_is_left_exactly_as_it_arrived() {
	cases := []struct {
		name string
		body string
	}{
		{"a message mentioning a source name in its own data", `{"note":"aws:s3 migration"}`},
		{"a message that is not JSON at all", `plain text`},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(tc.body), "")

			s.Require().NoError(err)
			s.Equal(tc.body, string(batch.Records[0].Body))
			s.Equal("queue", batch.SourceType)
			s.Empty(batch.Records[0].EventType)
		})
	}
}

func (s *ConsumerTestSuite) Test_failures_are_reported_per_message() {
	res := &handler.BatchResult{}
	res.Fail("msg-2", assertionError("could not reach the pricing service"))

	out, err := s.mapper.FromBatchResult(res)

	s.Require().NoError(err)
	response, ok := out.(events.SQSEventResponse)
	s.Require().True(ok)
	// Naming a message leaves it on the queue; naming none deletes the batch.
	// So this decides delivery rather than only reporting what happened.
	s.Require().Len(response.BatchItemFailures, 1)
	s.Equal("msg-2", response.BatchItemFailures[0].ItemIdentifier)
}

func (s *ConsumerTestSuite) Test_a_batch_that_all_succeeded_names_nothing() {
	cases := []struct {
		name string
		res  *handler.BatchResult
	}{
		{"an empty result", &handler.BatchResult{}},
		{"no result at all", nil},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			out, err := s.mapper.FromBatchResult(tc.res)

			s.Require().NoError(err)
			response, ok := out.(events.SQSEventResponse)
			s.Require().True(ok)
			s.Empty(response.BatchItemFailures)
			s.NotNil(response.BatchItemFailures,
				"SQS reads an absent list differently from an empty one")
		})
	}
}

func (s *ConsumerTestSuite) sqsEvent(body string) []byte {
	payload, err := json.Marshal(events.SQSEvent{Records: []events.SQSMessage{{
		MessageId:      "msg-1",
		Body:           body,
		EventSource:    "aws:sqs",
		EventSourceARN: "arn:aws:sqs:eu-west-2:123456789012:orders-queue",
		AWSRegion:      "eu-west-2",
	}}})
	s.Require().NoError(err)
	return payload
}

type assertionError string

func (e assertionError) Error() string { return string(e) }

func (s *ConsumerTestSuite) Test_an_unwrapped_events_own_resource_names_the_source() {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			// The batch reports SourceType "bucket", so naming the queue that
			// carried the notification would disagree with it.
			name: "a bucket notification names the bucket",
			body: `{"Records":[{"eventSource":"aws:s3","eventName":"ObjectCreated:Put",` +
				`"s3":{"bucket":{"name":"reports"},"object":{"key":"k"}}}]}`,
			want: "reports",
		},
		{
			name: "a stream record names the table, read out of the stream ARN",
			body: `{"eventSource":"aws:dynamodb","eventName":"INSERT",` +
				`"eventSourceARN":"arn:aws:dynamodb:eu-west-2:1:table/orders/stream/2026-09-18T00:00:00.000",` +
				`"dynamodb":{"Keys":{"id":{"S":"1"}}}}`,
			want: "orders",
		},
		{
			name: "an application's own message names the queue that delivered it",
			body: `{"orderId":"ord_42"}`,
			want: "orders-queue",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			// With no handler tag, the id that the blueprint gave the source is
			// preferred over all of this where there is one.
			batch, err := s.mapper.ToConsumerBatch(s.sqsEvent(tc.body), "")

			s.Require().NoError(err)
			s.Equal(tc.want, batch.SourceID)
		})
	}
}

func (s *ConsumerTestSuite) Test_the_batch_reports_the_source_it_was_delivered_by() {
	batch, err := s.mapper.ToConsumerBatch(fixture(&s.Suite, "sqs.json"), ordersTag)

	s.Require().NoError(err)
	s.JSONEq(`{"eventSource":"aws:sqs"}`, string(batch.Vendor))
}

func (s *ConsumerTestSuite) Test_one_batch_vendor_detail_is_not_shared_with_the_next() {
	// The field is a []byte the caller owns. Handing out one backing array
	// would let a handler that writes through it change what every later batch
	// in the same execution environment reports.
	first, err := s.mapper.ToConsumerBatch(fixture(&s.Suite, "sqs.json"), ordersTag)
	s.Require().NoError(err)

	first.Vendor[0] = 'X'

	second, err := s.mapper.ToConsumerBatch(fixture(&s.Suite, "sqs.json"), ordersTag)
	s.Require().NoError(err)
	s.JSONEq(`{"eventSource":"aws:sqs"}`, string(second.Vendor))
}
