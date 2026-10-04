package bucket

import (
	"encoding/base64"
	"fmt"

	"github.com/newstack-cloud/celerity-go-sdk/resources/bucket"
)

// Reports a cursor this package did not produce.
//
// A cursor reaches a handler from a client and comes back on the next request,
// so it is input an outsider chooses rather than something this package can
// assume it wrote.
func invalidCursor(err error) error {
	return fmt.Errorf("celerity: the cursor given is not one this store produced: %w", err)
}

// A bucket's position is the last key handed out, which is what S3 resumes
// from. Encoded the same way a query's is, though the two are separate types so
// that neither can be passed where the other is meant.
func encodeKeyAfter(key string) bucket.Cursor {
	return bucket.Cursor(base64.RawURLEncoding.EncodeToString([]byte(key)))
}

func keyAfter(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	key, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", invalidCursor(err)
	}
	return string(key), nil
}
