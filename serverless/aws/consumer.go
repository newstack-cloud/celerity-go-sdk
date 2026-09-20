package aws

import (
	"encoding/json"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

const (
	sqsEventSource = "aws:sqs"
	s3EventSource  = "aws:s3"
	ddbEventSource = "aws:dynamodb"
)

// Source types a consumer batch reports, matching the vocabulary the Celerity
// runtime uses, so a handler reads the same value on both.
const (
	sourceTypeQueue     = "queue"
	sourceTypeBucket    = "bucket"
	sourceTypeDatastore = "datastore"
)

// ToConsumerBatch maps an SQS event into a consumer batch.
//
// Every Celerity consumer arrives over SQS, including one bound to a bucket or
// a datastore, the notification is delivered to a queue, and the message body
// carries the originating event. Those are unwrapped so that a handler bound to
// a bucket is given the object event rather than an SQS message that happens to
// contain one, which is what it would receive under the Celerity runtime.
func (m Mapper) ToConsumerBatch(payload []byte, tag string) (*handler.ConsumerBatch, error) {
	event, err := decode[events.SQSEvent](payload, "SQS")
	if err != nil {
		return nil, err
	}

	records := make([]handler.ConsumerRecord, 0, len(event.Records))
	var origin sourceOrigin
	for i := range event.Records {
		record, recordOrigin := consumerRecord(&event.Records[i])
		if i == 0 {
			origin = recordOrigin
		}
		records = append(records, record)
	}

	return &handler.ConsumerBatch{
		Records:    records,
		SourceID:   sourceIDOf(tag, origin, event.Records),
		SourceType: origin.sourceType,
		Vendor:     batchVendor(),
	}, nil
}

var sqsBatchVendor = []byte(`{"eventSource":"` + sqsEventSource + `"}`)

// Returns a copy, since the field is a []byte the caller owns and a
// handler that writes through it would otherwise change what every later batch
// reports.
func batchVendor() []byte {
	out := make([]byte, len(sqsBatchVendor))
	copy(out, sqsBatchVendor)
	return out
}

type sourceOrigin struct {
	sourceType string
	// The name of the resource the event originated at, which for an unwrapped
	// event is a truer answer than the queue that carried it.
	name string
}

// Maps one SQS message, unwrapping an originating event where the
// body carries one, and reports what it turned out to have come from.
func consumerRecord(record *events.SQSMessage) (handler.ConsumerRecord, sourceOrigin) {
	out := handler.ConsumerRecord{
		MessageID:  record.MessageId,
		Body:       []byte(record.Body),
		Source:     record.EventSourceARN,
		Attributes: marshalOrNil(record.MessageAttributes),
		Vendor:     sqsVendor(record),
	}

	if unwrapped, ok := unwrapSourceEvent(record.Body); ok {
		out.Body = unwrapped.body
		out.EventType = unwrapped.eventType
		return out, unwrapped.origin
	}
	return out, sourceOrigin{sourceType: sourceTypeQueue}
}

// An originating event read out of the message that carried it.
type sourceEvent struct {
	body      []byte
	eventType string
	origin    sourceOrigin
}

// Reads an originating bucket or datastore event out of an SQS
// message body.
//
// A body that is not one is left exactly as it arrived as a queue or topic
// consumer's message is the application's own and nothing here should be
// reading it.
func unwrapSourceEvent(body string) (sourceEvent, bool) {
	if !strings.Contains(body, s3EventSource) && !strings.Contains(body, ddbEventSource) {
		// Cheaper than decoding, and the source names are what identify both
		// shapes, so a body without either cannot be one.
		return sourceEvent{}, false
	}

	if notification, matched := s3Notification(body); matched {
		mapped, err := notification.body()
		if err != nil {
			return sourceEvent{}, false
		}
		return sourceEvent{
			body:      mapped,
			eventType: bucketEventType(notification.EventName),
			origin: sourceOrigin{
				sourceType: sourceTypeBucket,
				name:       notification.S3.Bucket.Name,
			},
		}, true
	}

	if record, matched := dynamoDBStreamRecord(body); matched {
		mapped, err := record.body()
		if err != nil {
			return sourceEvent{}, false
		}
		return sourceEvent{
			body:      mapped,
			eventType: datastoreEventType(record.EventName),
			origin: sourceOrigin{
				sourceType: sourceTypeDatastore,
				name:       record.tableName(),
			},
		}, true
	}
	return sourceEvent{}, false
}

// An S3 event notification, which carries its records in the same
// envelope an SQS event does.
type s3Envelope struct {
	Records []s3Record `json:"Records"`
}

type s3Record struct {
	EventSource string `json:"eventSource"`
	EventName   string `json:"eventName"`
	S3          struct {
		Bucket struct {
			Name string `json:"name"`
		} `json:"bucket"`
		Object struct {
			Key  string `json:"key"`
			Size *int64 `json:"size"`
			ETag string `json:"eTag"`
		} `json:"object"`
	} `json:"s3"`
}

// Returns the object event as a handler sees it.
func (r s3Record) body() ([]byte, error) {
	out := map[string]any{"key": r.S3.Object.Key}

	if r.S3.Object.Size != nil {
		out["size"] = *r.S3.Object.Size
	}

	if r.S3.Object.ETag != "" {
		out["eTag"] = r.S3.Object.ETag
	}

	return json.Marshal(out)
}

func s3Notification(body string) (s3Record, bool) {
	var envelope s3Envelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return s3Record{}, false
	}

	if len(envelope.Records) == 0 || envelope.Records[0].EventSource != s3EventSource {
		return s3Record{}, false
	}

	return envelope.Records[0], true
}

func dynamoDBStreamRecord(body string) (streamRecord, bool) {
	var record streamRecord
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return streamRecord{}, false
	}

	if record.EventSource != ddbEventSource {
		return streamRecord{}, false
	}

	return record, true
}

// Maps S3's own event names onto Celerity's provider-agnostic
// vocabulary, so a handler reads the same event type whatever the bucket is.
//
// Both forms are accepted: a notification delivered to a queue names the
// event ObjectCreated:Put, and the same event configured on the bucket is
// s3:ObjectCreated:Put.
func bucketEventType(eventName string) string {
	name := strings.TrimPrefix(eventName, "s3:")

	switch {
	case strings.HasPrefix(name, "ObjectCreated:"), strings.HasPrefix(name, "ObjectRestore:"):
		return "created"
	case strings.HasPrefix(name, "ObjectRemoved:"):
		return "deleted"
	case strings.HasPrefix(name, "ObjectTagging:"), strings.HasPrefix(name, "ObjectAcl:"):
		return "metadataUpdated"
	default:
		return ""
	}
}

// Mames the source the batch came from.
//
// A handler tag has the form source::{sourceId}::{handlerName}, so the id the
// blueprint gave the source is already in it, and it is the same id the
// Celerity runtime reports. It is preferred over everything else for that
// reason.
//
// Failing that, the resource the event originated at. For an unwrapped event
// this would be the bucket or the table, which is what SourceType already says the batch came
// from, and otherwise the queue that delivered it. Both are AWS names rather
// than the blueprint's.
func sourceIDOf(tag string, origin sourceOrigin, records []events.SQSMessage) string {
	if rest, found := strings.CutPrefix(tag, "source::"); found {
		if sourceID, _, ok := strings.Cut(rest, "::"); ok {
			return sourceID
		}
	}

	if origin.name != "" {
		return origin.name
	}

	if len(records) > 0 {
		return arnResourceSegment(records[0].EventSourceARN, "")
	}

	return ""
}

func sqsVendor(record *events.SQSMessage) []byte {
	return marshalOrNil(map[string]any{
		"receiptHandle": record.ReceiptHandle,
		"attributes":    record.Attributes,
		"md5OfBody":     record.Md5OfBody,
		"eventSource":   record.EventSource,
		"awsRegion":     record.AWSRegion,
	})
}

// FromBatchResult maps a batch result into the partial-failure response SQS
// reads.
//
// Naming a message leaves it on the queue to be retried or redriven, and
// naming none deletes the batch, so this is what decides delivery rather than
// only reporting what happened. The function must be configured with
// ReportBatchItemFailures for SQS to read it at all; without that, a batch
// where one message failed is deleted whole.
func (m Mapper) FromBatchResult(res *handler.BatchResult) (any, error) {
	out := events.SQSEventResponse{BatchItemFailures: []events.SQSBatchItemFailure{}}
	if res == nil {
		return out, nil
	}

	for _, failure := range res.Failures {
		out.BatchItemFailures = append(out.BatchItemFailures,
			events.SQSBatchItemFailure{ItemIdentifier: failure.MessageID})
	}

	return out, nil
}

// Returns the resource an ARN names, after an optional
// prefix within the resource part.
//
//	arn:aws:sqs:eu-west-2:1:orders              ""       -> orders
//	arn:aws:dynamodb:eu-west-2:1:table/orders/… "table/" -> orders
func arnResourceSegment(arn, prefix string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return ""
	}
	resource := parts[5]

	if prefix != "" {
		rest, found := strings.CutPrefix(resource, prefix)
		if !found {
			return ""
		}
		resource = rest
	}
	name, _, _ := strings.Cut(resource, "/")
	return name
}

func marshalOrNil(v any) []byte {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return encoded
}
