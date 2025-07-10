// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file contains functions that infer a schema from a Go type.

package jsonschema

import (
	"fmt"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/internal/util"
)

// For constructs a JSON schema object for the given type argument.
//
// It translates Go types into compatible JSON schema types, as follows:
//   - Strings have schema type "string".
//   - Bools have schema type "boolean".
//   - Signed and unsigned integer types have schema type "integer".
//   - Floating point types have schema type "number".
//   - Slices and arrays have schema type "array", and a corresponding schema
//     for items.
//   - Maps with string key have schema type "object", and corresponding
//     schema for additionalProperties.
//   - Structs have schema type "object", and disallow additionalProperties.
//     Their properties are derived from exported struct fields, using the
//     struct field JSON name. Fields that are marked "omitempty" are
//     considered optional; all other fields become required properties.
//
// For returns an error if t contains (possibly recursively) any of the following Go
// types, as they are incompatible with the JSON schema spec.
//   - maps with key other than 'string'
//   - function types
//   - complex numbers
//   - unsafe pointers
//
// The types must not have cycles.
// It will return an error if there is a cycle in the types.
func For[T any]() (*Schema, error) {
	// TODO: consider skipping incompatible fields, instead of failing.
	seen := make(map[reflect.Type]bool)
	s, err := forType(reflect.TypeFor[T](), seen)
	if err != nil {
		var z T
		return nil, fmt.Errorf("For[%T](): %w", z, err)
	}
	return s, nil
}

func forType(t reflect.Type, seen map[reflect.Type]bool) (*Schema, error) {
	// Follow pointers: the schema for *T is almost the same as for T, except that
	// an explicit JSON "null" is allowed for the pointer.
	allowNull := false
	for t.Kind() == reflect.Pointer {
		allowNull = true
		t = t.Elem()
	}

	// Check for cycles
	// User defined types have a name, so we can skip those that are natively defined
	if t.Name() != "" {
		if seen[t] {
			return nil, fmt.Errorf("cycle detected for type %v", t)
		}
		seen[t] = true
		defer delete(seen, t)
	}

	var (
		s   = new(Schema)
		err error
	)

	switch t.Kind() {
	case reflect.Bool:
		s.Type = "boolean"

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr:
		s.Type = "integer"

	case reflect.Float32, reflect.Float64:
		s.Type = "number"

	case reflect.Interface:
		// Unrestricted

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %v", t.Key().Kind())
		}
		s.Type = "object"
		s.AdditionalProperties, err = forType(t.Elem(), seen)
		if err != nil {
			return nil, fmt.Errorf("computing map value schema: %v", err)
		}

	case reflect.Slice, reflect.Array:
		s.Type = "array"
		s.Items, err = forType(t.Elem(), seen)
		if err != nil {
			return nil, fmt.Errorf("computing element schema: %v", err)
		}
		if t.Kind() == reflect.Array {
			s.MinItems = Ptr(t.Len())
			s.MaxItems = Ptr(t.Len())
		}

	case reflect.String:
		s.Type = "string"

	case reflect.Struct:
		s.Type = "object"
		// no additional properties are allowed
		s.AdditionalProperties = falseSchema()

		for i := range t.NumField() {
			field := t.Field(i)
			info := util.FieldJSONInfo(field)
			if info.Omit {
				continue
			}
			if s.Properties == nil {
				s.Properties = make(map[string]*Schema)
			}
			s.Properties[info.Name], err = forType(field.Type, seen)
			if err != nil {
				return nil, err
			}
			if !info.Settings["omitempty"] && !info.Settings["omitzero"] {
				s.Required = append(s.Required, info.Name)
			}
		}

	default:
		return nil, fmt.Errorf("type %v is unsupported by jsonschema", t)
	}
	if allowNull && s.Type != "" {
		s.Types = []string{"null", s.Type}
		s.Type = ""
	}
	return s, nil
}
