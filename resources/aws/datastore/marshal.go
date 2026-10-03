package datastore

import (
	"bytes"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Items are marshalled by their json tags.
//
// An application's item type is not a DynamoDB type. The same struct is meant to
// work against Firestore and Cosmos DB, and is what `celerity schema codegen`
// emits from a schema that names no provider, so the tag it carries has to be
// the one every driver can read. That is `json`: the tag Go developers already
// write, and the one the other language SDKs' field names correspond to.
//
// attributevalue reads `dynamodbav` and ignores `json` unless TagKey says
// otherwise, so without this every field would be written under its Go name:
// CreatedAt where every other language writes createdAt, producing items no
// other SDK's types can read. Nothing would fail, which is what makes it worth
// a named option rather than a default nobody checks.
//
// `dynamodbav` still applies where a type carries one, so a handler
// that wants DynamoDB's own encoding for a field drops the json tag on it.
const itemTagKey = "json"

func encodeOptions(o *attributevalue.EncoderOptions) {
	o.TagKey = itemTagKey
}

func decodeOptions(o *attributevalue.DecoderOptions) {
	o.TagKey = itemTagKey
}

// Turns an application's item into attributes.
func marshalItem(item any) (map[string]dynamotypes.AttributeValue, error) {
	return attributevalue.MarshalMapWithOptions(item, encodeOptions)
}

// Fills out from one item's attributes.
func unmarshalItem(attrs map[string]dynamotypes.AttributeValue, out any) error {
	return attributevalue.UnmarshalMapWithOptions(attrs, out, decodeOptions)
}

// Fills out from a page of items.
func unmarshalItems(attrs []map[string]dynamotypes.AttributeValue, out any) error {
	return attributevalue.UnmarshalListOfMapsWithOptions(attrs, out, decodeOptions)
}

// Writes the key over an item, and refuses an item that has the key attribute
// set with a different value.
//
// The key is given separately from the item, so an item whose struct does not
// carry the key attributes is still addressable. Where it does carry them, a
// value that disagrees with the key is a caller intending something a put
// cannot do: a put replaces what is under a key, it does not move an item
// between keys. Overwriting silently would discard the change and answer that
// the write succeeded, which is the one failure nothing would notice.
//
// A key attribute the item leaves empty is not a disagreement. That is the
// ordinary case for a struct that carries the fields but does not set them, and
// for read-modify-write, where a read fills them from the stored item and they
// already agree.
func applyKey(
	attrs, keyAttrs map[string]dynamotypes.AttributeValue,
) error {
	for name, value := range keyAttrs {
		carried, ok := attrs[name]
		if ok && !emptyAttribute(carried) && !sameAttribute(carried, value) {
			return fmt.Errorf(
				"the item's %q is %s and the key says %s: a put replaces what is under a "+
					"key rather than moving an item, so pass the key the item is under",
				name, describeAttribute(carried), describeAttribute(value))
		}
		attrs[name] = value
	}
	return nil
}

// Reports whether an attribute holds nothing, which is a field a struct carries
// and did not set rather than a value a caller chose.
func emptyAttribute(value dynamotypes.AttributeValue) bool {
	switch typed := value.(type) {
	case *dynamotypes.AttributeValueMemberS:
		return typed.Value == ""
	case *dynamotypes.AttributeValueMemberN:
		return typed.Value == "" || typed.Value == "0"
	case *dynamotypes.AttributeValueMemberB:
		return len(typed.Value) == 0
	case *dynamotypes.AttributeValueMemberNULL:
		return true
	default:
		return false
	}
}

// Compares a key attribute the item carried against the key's own.
//
// Only the three kinds a key can be: anything else cannot be a key attribute,
// so it cannot agree with one either.
func sameAttribute(carried, key dynamotypes.AttributeValue) bool {
	switch left := carried.(type) {
	case *dynamotypes.AttributeValueMemberS:
		right, ok := key.(*dynamotypes.AttributeValueMemberS)
		return ok && left.Value == right.Value
	case *dynamotypes.AttributeValueMemberN:
		right, ok := key.(*dynamotypes.AttributeValueMemberN)
		return ok && left.Value == right.Value
	case *dynamotypes.AttributeValueMemberB:
		right, ok := key.(*dynamotypes.AttributeValueMemberB)
		return ok && bytes.Equal(left.Value, right.Value)
	default:
		return false
	}
}

// Names an attribute's value for an error a handler has to act on.
func describeAttribute(value dynamotypes.AttributeValue) string {
	switch typed := value.(type) {
	case *dynamotypes.AttributeValueMemberS:
		return fmt.Sprintf("%q", typed.Value)
	case *dynamotypes.AttributeValueMemberN:
		return typed.Value
	case *dynamotypes.AttributeValueMemberB:
		return fmt.Sprintf("%d bytes", len(typed.Value))
	default:
		return fmt.Sprintf("%T", value)
	}
}
