package celeritytest

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/resources/datastore"
)

// filtered reports whether an item survives a filter, with no filter keeping
// everything.
func filtered(condition *datastore.Condition, fields map[string]any) (bool, error) {
	if condition == nil {
		return true, nil
	}
	return matches(*condition, fields)
}

// matches evaluates a condition against an item's fields.
//
// Nil fields are an item that is not there, which is what [datastore.NotExists]
// is written to match and what every other test fails against.
func matches(condition datastore.Condition, fields map[string]any) (bool, error) {
	switch condition.Op {
	case datastore.OpAnd:
		return all(condition.Of, fields)
	case datastore.OpOr:
		return anyOf(condition.Of, fields)
	case datastore.OpExists:
		_, held := fields[condition.Name]
		return held, nil
	case datastore.OpNotExists:
		_, held := fields[condition.Name]
		return !held, nil
	}

	value, held := fields[condition.Name]
	if !held {
		// Every value test fails against a field that is not there, which is
		// what makes a conditional write on an absent item refuse rather than
		// match.
		return false, nil
	}
	return compare(condition, value)
}

func all(conditions []datastore.Condition, fields map[string]any) (bool, error) {
	for _, condition := range conditions {
		met, err := matches(condition, fields)
		if err != nil || !met {
			return false, err
		}
	}
	return true, nil
}

func anyOf(conditions []datastore.Condition, fields map[string]any) (bool, error) {
	for _, condition := range conditions {
		met, err := matches(condition, fields)
		if err != nil {
			return false, err
		}
		if met {
			return true, nil
		}
	}
	return false, nil
}

func compare(condition datastore.Condition, value any) (bool, error) {
	switch condition.Op {
	case datastore.OpEqual:
		return equal(value, condition.Value), nil
	case datastore.OpNotEqual:
		return !equal(value, condition.Value), nil
	case datastore.OpStartsWith:
		text, ok := value.(string)
		return ok && strings.HasPrefix(text, fmt.Sprint(condition.Value)), nil
	case datastore.OpContains:
		return contains(value, condition.Value), nil
	case datastore.OpBetween:
		low, lowOK := order(value, condition.Value)
		high, highOK := order(value, condition.High)
		return lowOK && highOK && low >= 0 && high <= 0, nil
	}

	direction, ok := order(value, condition.Value)
	if !ok {
		return false, nil
	}
	switch condition.Op {
	case datastore.OpLess:
		return direction < 0, nil
	case datastore.OpLessOrEqual:
		return direction <= 0, nil
	case datastore.OpGreater:
		return direction > 0, nil
	case datastore.OpGreaterOrEqual:
		return direction >= 0, nil
	}
	return false, fmt.Errorf("celerity: %w: %q", datastore.ErrInvalidCondition, condition.Op)
}

// equal compares a stored value with one a condition named.
//
// Stored values have been through JSON, so every number is a float64 whatever
// it was written as. Comparing the two as numbers is what stops an int written
// and an int tested from disagreeing.
func equal(stored, want any) bool {
	if left, right, ok := numbers(stored, want); ok {
		return left == right
	}
	return fmt.Sprint(stored) == fmt.Sprint(want)
}

// order reports how a stored value sorts against one a condition named, and
// whether the two can be ordered at all.
func order(stored, want any) (int, bool) {
	if left, right, ok := numbers(stored, want); ok {
		switch {
		case left < right:
			return -1, true
		case left > right:
			return 1, true
		default:
			return 0, true
		}
	}

	left, leftOK := stored.(string)
	right, rightOK := want.(string)
	if !leftOK || !rightOK {
		return 0, false
	}
	return strings.Compare(left, right), true
}

func numbers(stored, want any) (left, right float64, ok bool) {
	left, leftOK := asNumber(stored)
	right, rightOK := asNumber(want)
	return left, right, leftOK && rightOK
}

// A stored value is always float64, since an item is held as JSON. The breadth
// is for the other side of the comparison, the value a condition carries, which
// is whatever Go value the caller wrote.
//
// Switched on the kind rather than the type so that a named type reaches it too:
// a condition on Cents(500), where Cents is an int, is one the real store
// accepts because its marshaller reflects as well, and a type switch would call
// it not a number.
func asNumber(value any) (float64, bool) {
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(reflected.Uint()), true
	default:
		return 0, false
	}
}

// contains matches a substring of a string, or a member of a list.
func contains(stored, want any) bool {
	switch held := stored.(type) {
	case string:
		text, ok := want.(string)
		return ok && strings.Contains(held, text)
	case []any:
		for _, member := range held {
			if equal(member, want) {
				return true
			}
		}
	}
	return false
}

// sortMatches narrows a query to part of a partition.
//
// A sort key carries no attribute name, since the attribute is the store's, so
// this tests the key itself rather than a field of the item.
func sortMatches(condition datastore.SortCondition, sortKey string) bool {
	switch condition.Op {
	case datastore.OpEqual:
		return sortKey == condition.Value
	case datastore.OpLess:
		return sortKey < condition.Value
	case datastore.OpLessOrEqual:
		return sortKey <= condition.Value
	case datastore.OpGreater:
		return sortKey > condition.Value
	case datastore.OpGreaterOrEqual:
		return sortKey >= condition.Value
	case datastore.OpBetween:
		return sortKey >= condition.Value && sortKey <= condition.High
	case datastore.OpStartsWith:
		return strings.HasPrefix(sortKey, condition.Value)
	default:
		return false
	}
}
