// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// ReflectionValidator handles validation using dynamically created types.
// It uses SchemaTypeBuilder to create appropriate struct types for validation.
type ReflectionValidator struct {
	builder *SchemaTypeBuilder
}

// NewReflectionValidator creates a new ReflectionValidator with a SchemaTypeBuilder.
func NewReflectionValidator() *ReflectionValidator {
	return &ReflectionValidator{
		builder: NewSchemaTypeBuilder(),
	}
}

// SchemaValidationError represents an error that occurred during schema validation.
type SchemaValidationError struct {
	Operation string               // The operation that failed
	Schema    *jsonschema.Schema   // The schema being processed
	Resolved  *jsonschema.Resolved // The resolved schema
	Data      json.RawMessage      // The data being validated
	Cause     error                // The underlying error
}

// Error returns a formatted error message.
func (e *SchemaValidationError) Error() string {
	return fmt.Sprintf("schema validation failed during %s: %v", e.Operation, e.Cause)
}

// Unwrap returns the underlying error.
func (e *SchemaValidationError) Unwrap() error {
	return e.Cause
}

// ValidateAndApply validates data and applies defaults using reflection.
// It creates a typed struct from the schema, unmarshals directly into it for type validation,
// then converts to map format for applying defaults and final validation.
func (v *ReflectionValidator) ValidateAndApply(data json.RawMessage, resolved *jsonschema.Resolved) (json.RawMessage, error) {
	if resolved == nil {
		// If no schema is provided, return data as-is
		return data, nil
	}

	// Get the schema from the resolved schema
	schema := resolved.Schema()
	if schema == nil {
		return nil, &SchemaValidationError{
			Operation: "schema_extraction",
			Resolved:  resolved,
			Data:      data,
			Cause:     fmt.Errorf("resolved schema contains no schema definition"),
		}
	}

	// Build the struct type from the schema for type validation
	structType, err := v.builder.BuildType(schema)
	if err != nil {
		return nil, &SchemaValidationError{
			Operation: "schema_conversion",
			Schema:    schema,
			Resolved:  resolved,
			Data:      data,
			Cause:     err,
		}
	}

	var mapData map[string]any
	// Handle empty data case
	if len(data) == 0 {
		mapData = make(map[string]any)
	} else {
		// First, unmarshal into a map to preserve the original structure
		if err := json.Unmarshal(data, &mapData); err != nil {
			return nil, &SchemaValidationError{
				Operation: "unmarshaling",
				Schema:    schema,
				Resolved:  resolved,
				Data:      data,
				Cause:     fmt.Errorf("unmarshaling into map: %w", err),
			}
		}

		// Create a new instance of the struct type for type validation
		structValue := reflect.New(structType)
		structPtr := structValue.Interface()

		// Unmarshal directly into the typed struct for reflection-based type validation
		// This will fail if the types don't match (e.g., string where integer expected)
		if err := json.Unmarshal(data, structPtr); err != nil {
			return nil, &SchemaValidationError{
				Operation: "reflection_validation",
				Schema:    schema,
				Resolved:  resolved,
				Data:      data,
				Cause:     fmt.Errorf("reflection-based type validation failed: %w", err),
			}
		}
	}

	// Apply defaults using the resolved schema
	if err := resolved.ApplyDefaults(&mapData); err != nil {
		return nil, &SchemaValidationError{
			Operation: "applying_defaults",
			Schema:    schema,
			Resolved:  resolved,
			Data:      data,
			Cause:     fmt.Errorf("applying schema defaults: %w", err),
		}
	}

	// Validate the data with defaults applied
	if err := resolved.Validate(&mapData); err != nil {
		return nil, &SchemaValidationError{
			Operation: "validation",
			Schema:    schema,
			Resolved:  resolved,
			Data:      data,
			Cause:     err,
		}
	}

	// Marshal the final result with defaults applied
	result, err := json.Marshal(mapData)
	if err != nil {
		return nil, &SchemaValidationError{
			Operation: "final_marshaling",
			Schema:    schema,
			Resolved:  resolved,
			Data:      data,
			Cause:     fmt.Errorf("marshaling final result: %w", err),
		}
	}

	return result, nil
}
