package config

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ValuesFromJSON reads a store held as one JSON object of values.
//
// The shape several stores use: what the Celerity CLI writes into a local
// session, and what a secret holding a whole store contains. Read here rather
// than in each provider so that a value becomes the same text wherever it was
// held, which is what lets a handler read configuration without knowing.
//
// A store is seeded from a blueprint, and a blueprint's YAML gives numbers and
// booleans as themselves rather than as strings.
func ValuesFromJSON(raw []byte, source string) (map[string]string, error) {
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("celerity: the config store %q does not hold a JSON object: %w", source, err)
	}

	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = valueAsString(value)
	}
	return out, nil
}

func valueAsString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		// Formatted without an exponent and without a trailing .0, so a
		// blueprint's 3 reads as "3" rather than as "3e+00".
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	default:
		// An object or an array, which a handler decodes itself.
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}
