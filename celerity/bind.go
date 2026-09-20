package celerity

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/newstack-cloud/celerity-go-sdk/handler"
)

// bindRequest fills a typed request from an HTTP request.
//
// The body is decoded first, then struct tags override from the request line:
//
//	type GetOrder struct {
//	    OrderID string `path:"orderId"`
//	    Limit   int    `query:"limit"`
//	    TraceID string `header:"x-trace-id"`
//	}
//
// Binding after decoding rather than before means a path parameter always wins
// over a body field of the same name, which is the direction that cannot be
// spoofed by a caller.
func bindRequest(req *handler.Request, out any) error {
	if req == nil {
		return nil
	}
	if err := decodeInto(req.Body, out); err != nil {
		return err
	}

	v, ok := structToBind(out)
	if !ok {
		return nil
	}

	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		values, isPath, ok := taggedValue(req, field)
		if !ok {
			continue
		}
		if err := setField(v.Field(i), values, isPath); err != nil {
			return fmt.Errorf("binding %s: %w", field.Name, err)
		}
	}
	return nil
}

// Returns the struct to bind into, walking through however many
// pointers stand between out and it.
//
// A handler taking a pointer input, func(ctx, *CreateOrder), gives a pointer to
// a pointer here, so stopping at the first indirection would leave binding
// looking at a pointer and quietly doing nothing: the body would decode and
// every path, query and header tag would be ignored.
//
// A nil pointer along the way is allocated. json.Unmarshal allocates one where
// the body held an object, but an empty body is treated as absent, and a GET
// with no body still has path parameters to bind.
func structToBind(out any) (reflect.Value, bool) {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return reflect.Value{}, false
	}
	v = v.Elem()

	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			if !v.CanSet() {
				return reflect.Value{}, false
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	return v, true
}

// taggedValue returns every value bound to a field, and whether the tag it came
// from was a path tag.
//
// All of them, not the first: a catch-all path parameter yields one value per
// path segment, so taking the first would quietly truncate /files/a/b/c to a.
func taggedValue(req *handler.Request, field reflect.StructField) (handler.Values, bool, bool) {
	sources := []struct {
		tag    string
		params handler.Params
		isPath bool
	}{
		{"path", req.PathParams, true},
		{"query", req.QueryParams, false},
		{"header", req.Headers, false},
	}

	for _, source := range sources {
		name, ok := field.Tag.Lookup(source.tag)
		if !ok {
			continue
		}
		values, present := source.params[name]
		if !present || len(values) == 0 {
			return nil, source.isPath, false
		}
		return values, source.isPath, true
	}
	return nil, false, false
}

func setField(field reflect.Value, values handler.Values, isPath bool) error {
	if !field.CanSet() {
		return nil
	}

	// A slice field takes every value, which is how a handler reads all the
	// segments of a catch-all or every value of a repeated query parameter.
	if field.Kind() == reflect.Slice && field.Type().Elem().Kind() == reflect.String {
		slice := reflect.MakeSlice(field.Type(), len(values), len(values))
		for i, value := range values {
			slice.Index(i).SetString(value)
		}
		field.Set(slice)
		return nil
	}

	raw := values.First()
	// A catch-all bound to a string is the whole remaining path, so the
	// segments are rejoined rather than truncated to the first. Elsewhere a
	// repeated value keeps taking the first, since joining a repeated query
	// parameter with a separator would invent a syntax the caller never used.
	//
	// Rejoining is lossy where a segment contains an encoded separator:
	// /files/a/b%2Fc splits to ["a", "b/c"] and rejoins to "a/b/c", which reads
	// the same as /files/a/b/c. Bind to a []string to keep the split the runtime
	// made.
	if isPath && len(values) > 1 {
		raw = strings.Join(values, "/")
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(raw, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(parsed)
	default:
		return fmt.Errorf("cannot bind a %s from a request parameter", field.Kind())
	}
	return nil
}
