package config

import (
	"fmt"
	"reflect"
	"strings"
)

// AppendToSlice appends a new element to the slice at the given dotted
// key path. For string slices, args must contain exactly one value. For
// slices of struct elements, args must contain one value per exported
// field with a `yaml:` tag, in declaration order, each parsed according
// to its field type (string / bool / numeric scalars only).
//
// Returns an error if the path doesn't resolve to a slice, the element
// kind is unsupported, or the arg count doesn't match the element shape.
func (c *Config) AppendToSlice(path string, args []string) error {
	v, err := walkPath(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return err
	}
	if v.Kind() != reflect.Slice {
		return fmt.Errorf("config add %q: not a slice (it's %s)", path, v.Kind())
	}
	if !v.CanSet() {
		return fmt.Errorf("config add %q: slice is not settable", path)
	}
	elem, err := makeSliceElement(path, v.Type().Elem(), args)
	if err != nil {
		return err
	}
	v.Set(reflect.Append(v, elem))
	return nil
}

// RemoveFromSlice removes the 0-based index'th element from the slice
// at the given dotted key path.
func (c *Config) RemoveFromSlice(path string, index int) error {
	v, err := walkPath(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return err
	}
	if v.Kind() != reflect.Slice {
		return fmt.Errorf("config rm %q: not a slice (it's %s)", path, v.Kind())
	}
	if !v.CanSet() {
		return fmt.Errorf("config rm %q: slice is not settable", path)
	}
	n := v.Len()
	if index < 0 || index >= n {
		return fmt.Errorf("config rm %q: index %d out of range [0, %d)", path, index, n)
	}
	v.Set(reflect.AppendSlice(v.Slice(0, index), v.Slice(index+1, n)))
	return nil
}

// ListSlice returns the YAML rendering of the slice at the given dotted
// key path. Fails if the path resolves to a non-slice value so callers
// can distinguish "this is a list, use add/rm" from "this is a scalar,
// use get/set".
func (c *Config) ListSlice(path string) (string, error) {
	v, err := walkPath(reflect.ValueOf(c).Elem(), path)
	if err != nil {
		return "", err
	}
	if v.Kind() != reflect.Slice {
		return "", fmt.Errorf("config list %q: not a slice (it's %s); use 'config get' for non-slice values", path, v.Kind())
	}
	return renderValue(v)
}

// makeSliceElement builds a new slice element of the given type from
// positional args according to the rules documented on AppendToSlice.
func makeSliceElement(path string, t reflect.Type, args []string) (reflect.Value, error) {
	switch t.Kind() {
	case reflect.String:
		if len(args) != 1 {
			return reflect.Value{}, fmt.Errorf("config add %q: string slice expects 1 argument, got %d", path, len(args))
		}
		v := reflect.New(t).Elem()
		v.SetString(args[0])
		return v, nil
	case reflect.Struct:
		fields := exportedYAMLFields(t)
		if len(fields) == 0 {
			return reflect.Value{}, fmt.Errorf("config add %q: element type %s has no yaml-tagged fields", path, t.Name())
		}
		if len(args) != len(fields) {
			names := make([]string, len(fields))
			for i, f := range fields {
				names[i] = f.tag
			}
			return reflect.Value{}, fmt.Errorf("config add %q: %s element requires %d argument(s) (%s); got %d",
				path, t.Name(), len(fields), strings.Join(names, ", "), len(args))
		}
		v := reflect.New(t).Elem()
		for i, f := range fields {
			if err := coerceAndAssign(v.Field(f.index), args[i]); err != nil {
				return reflect.Value{}, fmt.Errorf("config add %q: parsing %s: %w", path, f.tag, err)
			}
		}
		return v, nil
	default:
		return reflect.Value{}, fmt.Errorf("config add %q: unsupported element kind %s", path, t.Kind())
	}
}

type yamlField struct {
	index int
	tag   string
}

// exportedYAMLFields returns the exported fields of t that carry a
// non-empty `yaml:"name"` tag, in declaration order.
func exportedYAMLFields(t reflect.Type) []yamlField {
	var fields []yamlField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _ := splitYAMLTag(f.Tag.Get("yaml"))
		if name == "" || name == "-" {
			continue
		}
		fields = append(fields, yamlField{index: i, tag: name})
	}
	return fields
}
