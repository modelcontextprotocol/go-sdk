// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// StrictUnmarshal unmarshals JSON data into v with strict validation rules:
// - Rejects duplicate keys with different cases (e.g., "name" and "Name")
// - Validates that JSON field names exactly match struct tags (case-sensitive)
// - Rejects unknown fields not defined in the struct
//
// This prevents message smuggling attacks that exploit Go's case-insensitive
// JSON unmarshalling behavior, which violates JSON-RPC 2.0 specification
// requirements for case-sensitive field matching.
func StrictUnmarshal(data []byte, v interface{}) error {
	// 1. Check for case-variant duplicate keys
	if err := validateNoDuplicateKeys(data); err != nil {
		return fmt.Errorf("strict unmarshal: %w", err)
	}

	// 2. Validate field names match struct tags exactly (case-sensitive)
	if err := validateFieldCase(data, v); err != nil {
		return fmt.Errorf("strict unmarshal: %w", err)
	}

	// 3. Use strict decoder that disallows unknown fields
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("strict unmarshal: %w", err)
	}

	return nil
}

// validateNoDuplicateKeys checks if the JSON data contains duplicate keys
// with different cases (e.g., both "name" and "Name").
func validateNoDuplicateKeys(data []byte) error {
	// Parse into a generic map to get all keys
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// If it's not an object, no duplicate keys are possible
		return nil
	}

	// Check for case-variant duplicates
	seen := make(map[string]string) // lowercase -> original
	for key := range raw {
		lowerKey := strings.ToLower(key)
		if original, exists := seen[lowerKey]; exists && original != key {
			return fmt.Errorf("duplicate key with different case: %q and %q", original, key)
		}
		seen[lowerKey] = key
	}

	// Recursively check nested objects and arrays
	for key, val := range raw {
		if err := validateNoDuplicateKeysRecursive(val); err != nil {
			return fmt.Errorf("in field %q: %w", key, err)
		}
	}

	return nil
}

// validateNoDuplicateKeysRecursive recursively validates nested JSON structures
func validateNoDuplicateKeysRecursive(data json.RawMessage) error {
	// Try to parse as object
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err == nil {
		// It's an object, check for duplicates
		seen := make(map[string]string)
		for key := range obj {
			lowerKey := strings.ToLower(key)
			if original, exists := seen[lowerKey]; exists && original != key {
				return fmt.Errorf("duplicate key with different case: %q and %q", original, key)
			}
			seen[lowerKey] = key
		}

		// Recursively check nested values
		for key, val := range obj {
			if err := validateNoDuplicateKeysRecursive(val); err != nil {
				return fmt.Errorf("in field %q: %w", key, err)
			}
		}
		return nil
	}

	// Try to parse as array
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil {
		// It's an array, check each element
		for i, elem := range arr {
			if err := validateNoDuplicateKeysRecursive(elem); err != nil {
				return fmt.Errorf("in array index %d: %w", i, err)
			}
		}
		return nil
	}

	// It's a primitive value, no duplicates possible
	return nil
}

// validateFieldCase ensures that JSON field names exactly match the struct
// tags (case-sensitive). This prevents attacks where an attacker sends
// "Name" instead of "name" to smuggle values.
func validateFieldCase(data []byte, v interface{}) error {
	// Get expected field names from struct tags
	expectedFields := extractExpectedFields(v)

	// Parse JSON to get actual field names
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// If it's not an object, nothing to validate
		return nil
	}

	// Check that all JSON keys match expected fields exactly
	for key := range raw {
		// Check if this key exists in expected fields (case-sensitive)
		if !expectedFields[key] {
			// Check if a case-insensitive match exists (which would be a smuggling attempt)
			lowerKey := strings.ToLower(key)
			for expected := range expectedFields {
				if strings.ToLower(expected) == lowerKey {
					return fmt.Errorf("field name case mismatch: got %q, expected %q", key, expected)
				}
			}
			// If no case-insensitive match, it's an unknown field
			// (will be caught by DisallowUnknownFields)
		}
	}

	return nil
}

// extractExpectedFields uses reflection to extract valid field names from
// struct tags. Returns a map of field names that are expected in the JSON.
func extractExpectedFields(v interface{}) map[string]bool {
	fields := make(map[string]bool)

	// Get the type, handling pointers
	t := reflect.TypeOf(v)
	if t == nil {
		return fields
	}

	// Dereference pointer types
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// Only structs have fields
	if t.Kind() != reflect.Struct {
		return fields
	}

	// Extract field names from json tags
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")

		// Parse json tag (format: "name,omitempty")
		if tag == "" || tag == "-" {
			continue
		}

		// Extract field name (before comma)
		name := tag
		if idx := strings.Index(tag, ","); idx != -1 {
			name = tag[:idx]
		}

		if name != "" {
			fields[name] = true
		}
	}

	return fields
}
