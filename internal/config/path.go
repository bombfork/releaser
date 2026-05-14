package config

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnknownKey is returned by Get/Set when a dotted key path does not
// resolve to any field or map entry in the Config schema.
var ErrUnknownKey = errors.New("unknown configuration key")

// ErrUnsettableKey is returned by Set when the resolved value cannot be
// set directly — for example when the path would descend through a slice.
// Slice elements are managed via the dedicated per-slice helpers.
var ErrUnsettableKey = errors.New("key is not directly settable; use the per-slice subcommands")

// Get returns the value at the given dotted key path. Scalar leaves
// (strings, bools, numbers) are returned as bare text; structs, maps, and
// slices are returned as YAML fragments. Slice index addressing is not
// supported — request the whole slice and use the per-slice subcommands
// for element-level operations.
func (c *Config) Get(key string) (string, error) {
	v, err := walkPath(reflect.ValueOf(c).Elem(), key)
	if err != nil {
		return "", err
	}
	return renderValue(v)
}

// walkPath descends through root following the dotted segments of key.
func walkPath(root reflect.Value, key string) (reflect.Value, error) {
	if key == "" {
		return reflect.Value{}, errors.New("config: empty key")
	}
	v := root
	for _, seg := range strings.Split(key, ".") {
		next, err := stepValue(v, seg)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("config %q: %w", key, err)
		}
		v = next
	}
	return v, nil
}

// stepValue takes a single step from v into the field, map entry, or
// (rejected) slice element named by seg.
func stepValue(v reflect.Value, seg string) (reflect.Value, error) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}, fmt.Errorf("nil pointer at segment %q", seg)
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		idx, ok := fieldByYAMLTag(v.Type(), seg)
		if !ok {
			return reflect.Value{}, fmt.Errorf("segment %q: %w", seg, ErrUnknownKey)
		}
		return v.Field(idx), nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return reflect.Value{}, fmt.Errorf("segment %q: map key is %s, not string", seg, v.Type().Key().Kind())
		}
		mv := v.MapIndex(reflect.ValueOf(seg))
		if !mv.IsValid() {
			return reflect.Value{}, fmt.Errorf("segment %q: %w", seg, ErrUnknownKey)
		}
		return mv, nil
	case reflect.Slice:
		return reflect.Value{}, fmt.Errorf("segment %q: cannot address slice elements by path; use the per-slice subcommands", seg)
	default:
		return reflect.Value{}, fmt.Errorf("segment %q: cannot descend into %s", seg, v.Kind())
	}
}

// fieldByYAMLTag returns the index of the struct field tagged `yaml:"name"`,
// honoring the comma-separated tag form (e.g. `yaml:"name,omitempty"`).
func fieldByYAMLTag(t reflect.Type, name string) (int, bool) {
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" {
			continue
		}
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		if tag == name {
			return i, true
		}
	}
	return 0, false
}

// Set assigns the given value to the leaf at the given dotted key path.
// Only scalar leaves (string-, bool-, number-typed) and map entries can be
// set. Whole structs, maps, and slices are not settable through this API
// — set the individual entries or use the per-slice subcommands.
//
// Set mutates the receiver only; persisting the change is the caller's
// responsibility (Save). Validation against the adapter is also the
// caller's responsibility — Set itself accepts any syntactically valid
// value, including empty strings.
func (c *Config) Set(key, value string) error {
	if key == "" {
		return errors.New("config: empty key")
	}
	segments := strings.Split(key, ".")
	return setRecursive(reflect.ValueOf(c).Elem(), segments, value)
}

// setRecursive descends through v following segments, assigning value at
// the final segment. Intermediate nil maps are lazily initialized.
func setRecursive(v reflect.Value, segments []string, value string) error {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return fmt.Errorf("nil pointer at %q", segments[0])
		}
		v = v.Elem()
	}
	seg := segments[0]
	rest := segments[1:]

	switch v.Kind() {
	case reflect.Struct:
		idx, ok := fieldByYAMLTag(v.Type(), seg)
		if !ok {
			return fmt.Errorf("config set %q: %w", seg, ErrUnknownKey)
		}
		field := v.Field(idx)
		if !field.CanSet() {
			return fmt.Errorf("config set %q: field not settable", seg)
		}
		if len(rest) == 0 {
			switch field.Kind() {
			case reflect.Slice:
				return fmt.Errorf("config set %q: %w", seg, ErrUnsettableKey)
			case reflect.Map:
				return fmt.Errorf("config set %q: set individual entries with %s.<key>", seg, seg)
			}
			return coerceAndAssign(field, value)
		}
		if field.Kind() == reflect.Map && field.IsNil() {
			field.Set(reflect.MakeMap(field.Type()))
		}
		return setRecursive(field, rest, value)

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("config set: map key kind %s, not string", v.Type().Key().Kind())
		}
		if len(rest) > 0 {
			return fmt.Errorf("config set %q: nested addressing into map entries is not supported", seg)
		}
		newVal := reflect.New(v.Type().Elem()).Elem()
		if err := coerceAndAssign(newVal, value); err != nil {
			return fmt.Errorf("config set %q: %w", seg, err)
		}
		v.SetMapIndex(reflect.ValueOf(seg), newVal)
		return nil

	case reflect.Slice:
		return fmt.Errorf("config set %q: cannot address slice elements by path; use the per-slice subcommands", seg)
	}
	return fmt.Errorf("config set: cannot descend into %s", v.Kind())
}

// coerceAndAssign parses value according to dst's kind and assigns it.
// dst must be a settable scalar (string/bool/int family/float family).
func coerceAndAssign(dst reflect.Value, value string) error {
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(value)
		return nil
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parse bool %q: %w", value, err)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse int %q: %w", value, err)
		}
		dst.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse uint %q: %w", value, err)
		}
		dst.SetUint(n)
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(value, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse float %q: %w", value, err)
		}
		dst.SetFloat(f)
		return nil
	}
	return fmt.Errorf("unsupported value kind: %s", dst.Kind())
}

// renderValue converts a reflect.Value to its CLI text representation.
// Scalars become bare text; composite values become YAML fragments with
// no trailing newline.
func renderValue(v reflect.Value) (string, error) {
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64), nil
	}
	data, err := yaml.Marshal(v.Interface())
	if err != nil {
		return "", fmt.Errorf("render value: %w", err)
	}
	return strings.TrimRight(string(data), "\n"), nil
}
