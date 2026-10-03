package datastore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// A cursor is where a listing stopped, in a form a handler can hand to a client
// and take back on the next request.
//
// Encoded rather than given out as it stands, for two reasons. Firstly, it travels, a
// paged API puts it in a query string, and DynamoDB's own position is a map of
// typed attributes. And it is nobody's business but this package's, so encoding
// it says so, and a client that takes one apart and rebuilds it has done
// something this package is free to break.
//
// Base64url, so it survives a URL without escaping.

// keyValue is one attribute of a position, with the type it was declared as.
//
// Carried rather than inferred, because a table keyed on a number and one keyed
// on the string of that number are different tables, and resuming with the
// wrong one is refused.
type keyValue struct {
	Type  string `json:"t"`
	Value string `json:"v"`
}

func encodeCursor(key map[string]dynamotypes.AttributeValue) (datastore.Cursor, error) {
	if len(key) == 0 {
		return "", nil
	}

	position := make(map[string]keyValue, len(key))
	for name, value := range key {
		switch typed := value.(type) {
		case *dynamotypes.AttributeValueMemberS:
			position[name] = keyValue{Type: "S", Value: typed.Value}
		case *dynamotypes.AttributeValueMemberN:
			position[name] = keyValue{Type: "N", Value: typed.Value}
		case *dynamotypes.AttributeValueMemberB:
			position[name] = keyValue{
				Type:  "B",
				Value: base64.StdEncoding.EncodeToString(typed.Value),
			}
		default:
			// A key attribute is a string, a number or binary, and DynamoDB
			// puts nothing else in a position. Anything else means the shape
			// changed under this package rather than that the caller did
			// something.
			return "", fmt.Errorf(
				"celerity: resuming is not possible: DynamoDB gave a position holding "+
					"%q, which is not a kind of key attribute", name,
			)
		}
	}

	encoded, err := json.Marshal(position)
	if err != nil {
		return "", fmt.Errorf("celerity: encoding where a query stopped: %w", err)
	}
	return datastore.Cursor(base64.RawURLEncoding.EncodeToString(encoded)), nil
}

func decodeCursor(cursor string) (map[string]dynamotypes.AttributeValue, error) {
	if cursor == "" {
		return nil, nil
	}

	encoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, invalidCursor(err)
	}
	var position map[string]keyValue
	if err := json.Unmarshal(encoded, &position); err != nil {
		return nil, invalidCursor(err)
	}

	key := make(map[string]dynamotypes.AttributeValue, len(position))
	for name, value := range position {
		switch value.Type {
		case "S":
			key[name] = &dynamotypes.AttributeValueMemberS{Value: value.Value}
		case "N":
			key[name] = &dynamotypes.AttributeValueMemberN{Value: value.Value}
		case "B":
			raw, err := base64.StdEncoding.DecodeString(value.Value)
			if err != nil {
				return nil, invalidCursor(err)
			}
			key[name] = &dynamotypes.AttributeValueMemberB{Value: raw}
		default:
			return nil, invalidCursor(fmt.Errorf("%q is not a kind of key attribute", value.Type))
		}
	}
	return key, nil
}

// Names what the argument was rather than what it failed to
// parse as, since a cursor reaches a handler from a client and is the one
// input here that an outsider chooses.
func invalidCursor(err error) error {
	return fmt.Errorf("%w: %w", datastore.ErrInvalidCursor, err)
}

// refusedCursor is a cursor DynamoDB would not resume from.
//
// A cursor that decodes is not yet one this query can use, a client that edits
// the partition inside one produces a position DynamoDB validates against the
// key condition and refuses. That is the same mistake as a malformed cursor and
// is reported as the same thing, rather than as the query having failed, so a
// handler answers a bad request rather than a server error.
//
// Only asked where a cursor was given. Without that, a validation failure about
// something else entirely would be blamed on a cursor that was never there.
func refusedCursor(err error, cursor string) error {
	if cursor == "" {
		return err
	}
	var api smithy.APIError
	if !errors.As(err, &api) || api.ErrorCode() != "ValidationException" {
		return err
	}
	// The service has several messages for this and no distinct error code, so
	// the position it names is what identifies them.
	message := api.ErrorMessage()
	if !strings.Contains(message, "starting key") &&
		!strings.Contains(message, "ExclusiveStartKey") {
		return err
	}
	return fmt.Errorf("%w: %w", datastore.ErrInvalidCursor, err)
}
