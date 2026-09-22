package config

import (
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// Fills a struct from a store's values, by `config` tag:
//
//	type settings struct {
//	    Region  string        `config:"REGION"`
//	    Retries int           `config:"RETRIES"`
//	    Timeout time.Duration `config:"TIMEOUT"`
//	}
//
// A tagged struct field is a prefix on the keys of the fields within it rather
// than a value of its own, so configuration can be grouped the way the
// application would structure it:
//
//	type settings struct {
//	    Database struct {
//	        Host string `config:"HOST"`
//	        Port int    `config:"PORT"`
//	    } `config:"DATABASE"`
//	}
//
// reads DATABASE_HOST and DATABASE_PORT, and would equally read
// DATABASE/HOST. Neither separator is special, a prefix is followed
// by either, decided per level, because a parameter store holds a hierarchy as
// a path while the Celerity CLI writes compound keys with an underscore and one
// key can use both. What follows the last prefix is one name however it is
// punctuated, so pricing_url is a field rather than two levels.
//
// An embedded struct is not a level. Its fields are promoted, so they bind as
// though they had been declared at the higher level, which is how configuration held in
// common is shared.
//
// A key the store does not hold leaves its field alone, so a struct built with
// defaults keeps them. A value that will not parse is an error naming the key
// and what it could not become, since a configuration that is present and
// wrong should stop an application rather than silently read as zero.
func bindValues(values map[string]string, out any) error {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("celerity: config can only be bound into a pointer to a struct")
	}
	v = v.Elem()

	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			if !v.CanSet() {
				return fmt.Errorf("celerity: config can only be bound into a pointer to a struct")
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return fmt.Errorf("celerity: config can only be bound into a pointer to a struct")
	}

	return bindStruct(values, v, nil)
}

// bindStruct fills one struct's tagged fields, under the prefixes it is nested
// beneath.
//
// A tagged struct field is a prefix and is descended into; a tagged field of
// any other type is a value and is read. An embedded struct is not a prefix, since
// its fields are promoted to this one.
//
// prefixes holds every key a field here could be written under, because the
// separator is a property of the key rather than of the store. A level written
// with a slash can sit above one written with an underscore. Empty at the top,
// where a field's tag is the whole key.
func bindStruct(values map[string]string, v reflect.Value, prefixes []string) error {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)

		if field.Anonymous {
			if err := bindEmbedded(values, v.Field(i)); err != nil {
				return err
			}
			continue
		}

		tag, tagged := field.Tag.Lookup("config")
		if !tagged || !field.IsExported() {
			continue
		}

		if group, ok := groupToDescend(v.Field(i)); ok {
			if err := bindStruct(values, group, candidates(prefixes, tag)); err != nil {
				return err
			}
			continue
		}

		raw, key, present := lookup(values, prefixes, tag)
		if !present {
			continue
		}
		if err := setConfigField(v.Field(i), raw); err != nil {
			return fmt.Errorf("celerity: config %q: %w", key, err)
		}
	}
	return nil
}

var separators = []string{"_", "/"}

// Returns every key a tag could be written under, given every key
// the level above could be written under.
//
// Two per level rather than one for the whole key, because a level's separator
// says nothing about the next one's. At realistic depths this is a handful of
// map lookups, and it is what lets a struct describe a hierarchy without also
// having to know how each level of it was punctuated.
func candidates(prefixes []string, tag string) []string {
	if len(prefixes) == 0 {
		return []string{tag}
	}

	out := make([]string, 0, len(prefixes)*len(separators))
	for _, prefix := range prefixes {
		for _, separator := range separators {
			out = append(out, prefix+separator+tag)
		}
	}
	return out
}

// lookup finds the value for a field under any of the keys it could be written
// as, and reports the key it was found under so an error can name it.
func lookup(values map[string]string, prefixes []string, tag string) (string, string, bool) {
	keys := candidates(prefixes, tag)
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			return raw, key, true
		}
	}
	// The first is the one an underscore throughout would produce, which is
	// what the tooling writes, so an error names the likeliest spelling.
	return "", keys[0], false
}

// Reports a field that groups configuration rather than holding
// a value, and returns the struct to descend into.
//
// A time.Duration is an integer and a time.Time is a struct with nothing
// tagged in it, so what decides is whether the type is a struct this package
// reads as a value.
func groupToDescend(v reflect.Value) (reflect.Value, bool) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			if !v.CanSet() {
				return reflect.Value{}, false
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct ||
		v.Type() == reflect.TypeFor[time.Time]() {
		return reflect.Value{}, false
	}

	return v, true
}

// Fills what is embedded in a struct, through however many
// pointers it was embedded behind.
//
// A nil embedded pointer is allocated, since the fields it holds are promoted
// whether or not anything has built it yet.
//
// One that cannot be allocated is refused rather than skipped. An embedded
// pointer to an unexported type is unreachable through reflection, so its
// promoted fields can never be filled, and a caller who tagged them would
// otherwise read zero values and be told nothing. Encoding/json refuses the
// same shape for the same reason.
func bindEmbedded(values map[string]string, v reflect.Value) error {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			if !v.CanSet() {
				return fmt.Errorf(
					"celerity: config cannot be bound into an embedded pointer to the "+
						"unexported type %s: export the type, or embed it by value",
					v.Type().Elem(),
				)
			}
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		// An embedded type that is not a struct, such as an embedded named
		// string. It has no fields of its own to bind, and a tag on the
		// embedding is not a key this reads.
		return nil
	}
	return bindStruct(values, v, nil)
}

func setConfigField(field reflect.Value, raw string) error {
	if !field.CanSet() {
		return nil
	}

	// Ahead of the integer cases, a Duration is an int64, and reading "30s" as
	// one would fail where what was meant is plain.
	if field.Type() == reflect.TypeFor[time.Duration]() {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%q is not a duration, such as 30s or 500ms", raw)
		}
		field.Set(reflect.ValueOf(parsed))
		return nil
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%q is not true or false", raw)
		}
		field.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a whole number", raw)
		}
		field.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(raw, 10, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a whole number that is not negative", raw)
		}
		field.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(raw, field.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", raw)
		}
		field.SetFloat(parsed)
	default:
		return fmt.Errorf("a %s cannot be read from a configuration value", field.Kind())
	}
	return nil
}
