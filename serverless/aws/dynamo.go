package aws

import (
	"encoding/json"
	"fmt"
)

// DynamoDB describes item attributes in a JSON form of its own, tagging each
// value with its type: {"id": {"S": "abc"}, "count": {"N": "3"}}. A handler
// should see the item, not the tagging, so this translates one into the other.
//
// Decoded here rather than with the AWS SDK's own helper, which reaches it
// through the DynamoDB service types and so would compile two service clients
// into a module whose purpose is to stay small.

// One tagged value where exactly one field is set.
type attributeValue struct {
	S    *string                   `json:"S"`
	N    *json.Number              `json:"N"`
	B    *string                   `json:"B"`
	Bool *bool                     `json:"BOOL"`
	Null *bool                     `json:"NULL"`
	M    map[string]attributeValue `json:"M"`
	L    []attributeValue          `json:"L"`
	SS   []string                  `json:"SS"`
	NS   []json.Number             `json:"NS"`
	BS   []string                  `json:"BS"`
}

// Returns the value without its type tag.
//
// A number becomes a [json.Number] rather than a float64 as DynamoDB carries
// numbers as decimal text of up to 38 significant digits, and a float64 holds
// about 16, so converting would round an id or a currency amount to something
// the caller never stored. Encoded back into JSON it is the same digits it
// arrived as, and a handler that needs arithmetic asks for the precision it
// wants.
//
// Binary stays base64 text, which is how it arrived and how it re-encodes.
func (a attributeValue) plainValue() any {
	switch {
	case a.S != nil:
		return *a.S
	case a.N != nil:
		return *a.N
	case a.Bool != nil:
		return *a.Bool
	case a.Null != nil:
		return nil
	case a.B != nil:
		return *a.B
	case a.M != nil:
		return plainItem(a.M)
	case a.L != nil:
		list := make([]any, 0, len(a.L))
		for _, item := range a.L {
			list = append(list, item.plainValue())
		}
		return list
	case a.SS != nil:
		return a.SS
	case a.NS != nil:
		return a.NS
	case a.BS != nil:
		return a.BS
	default:
		// A tag this does not know, which the format can gain. Reported as
		// absent rather than guessed at.
		return nil
	}
}

func plainItem(item map[string]attributeValue) map[string]any {
	if item == nil {
		return nil
	}
	out := make(map[string]any, len(item))
	for name, value := range item {
		out[name] = value.plainValue()
	}
	return out
}

// A DynamoDB stream record as it arrives inside an SQS message
// body.
type streamRecord struct {
	EventSource  string `json:"eventSource"`
	EventName    string `json:"eventName"`
	EventSourceA string `json:"eventSourceARN"`
	DynamoDB     *struct {
		Keys     map[string]attributeValue `json:"Keys"`
		NewImage map[string]attributeValue `json:"NewImage"`
		OldImage map[string]attributeValue `json:"OldImage"`
	} `json:"dynamodb"`
}

// Returns the record as a handler sees it, this includes the keys
// and images with their type tags resolved.
func (r streamRecord) body() ([]byte, error) {
	out := map[string]any{"keys": map[string]any{}}
	if r.DynamoDB == nil {
		return json.Marshal(out)
	}

	if item := plainItem(r.DynamoDB.Keys); item != nil {
		out["keys"] = item
	}
	if item := plainItem(r.DynamoDB.NewImage); item != nil {
		out["newItem"] = item
	}
	if item := plainItem(r.DynamoDB.OldImage); item != nil {
		out["oldItem"] = item
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("composing a datastore event body: %w", err)
	}
	return body, nil
}

// Returns the table a stream record came from, read out of the stream
// ARN, which has the form
// arn:aws:dynamodb:{region}:{account}:table/{table}/stream/{timestamp}.
func (r streamRecord) tableName() string {
	return arnResourceSegment(r.EventSourceA, "table/")
}

// Maps DynamoDB's own event names onto Celerity's
// provider-agnostic vocabulary, so a handler reads the same event type whatever
// the datastore is.
func datastoreEventType(eventName string) string {
	switch eventName {
	case "INSERT":
		return "inserted"
	case "MODIFY":
		return "modified"
	case "REMOVE":
		return "removed"
	default:
		return ""
	}
}
