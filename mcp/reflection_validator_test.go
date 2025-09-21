// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestNewReflectionValidator(t *testing.T) {
	validator := NewReflectionValidator()
	if validator == nil {
		t.Fatal("NewReflectionValidator returned nil")
	}
	if validator.builder == nil {
		t.Fatal("ReflectionValidator builder is nil")
	}
}

func TestSchemaValidationError(t *testing.T) {
	cause := errors.New("underlying error")
	err := &SchemaValidationError{
		Operation: "test_operation",
		Cause:     cause,
	}

	// Test Error method
	errorMsg := err.Error()
	if !strings.Contains(errorMsg, "test_operation") {
		t.Errorf("Error message should contain operation: %s", errorMsg)
	}
	if !strings.Contains(errorMsg, "underlying error") {
		t.Errorf("Error message should contain cause: %s", errorMsg)
	}

	// Test Unwrap method
	if err.Unwrap() != cause {
		t.Errorf("Unwrap should return the cause error")
	}
}

func TestReflectionValidator_ValidateAndApply_NilResolved(t *testing.T) {
	validator := NewReflectionValidator()
	data := json.RawMessage(`{"test": "value"}`)

	result, err := validator.ValidateAndApply(data, nil)
	if err != nil {
		t.Fatalf("Expected no error with nil resolved, got: %v", err)
	}

	if string(result) != string(data) {
		t.Errorf("Expected data to be returned as-is, got: %s", result)
	}
}

func TestReflectionValidator_ValidateAndApply_SimpleObject(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a simple object schema
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer"}
		},
		"required": ["name"]
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test valid data
	data := json.RawMessage(`{"name": "John", "age": 30}`)
	result, err := validator.ValidateAndApply(data, resolved)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify the result contains the expected data
	var resultMap map[string]any
	if err := json.Unmarshal(result, &resultMap); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if resultMap["name"] != "John" {
		t.Errorf("Expected name to be 'John', got: %v", resultMap["name"])
	}
	if resultMap["age"] != float64(30) { // JSON numbers are float64
		t.Errorf("Expected age to be 30, got: %v", resultMap["age"])
	}
}

func TestReflectionValidator_ValidateAndApply_WithDefaults(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a schema with default values
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"status": {"type": "string", "default": "active"}
		},
		"required": ["name"]
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test data without the default field
	data := json.RawMessage(`{"name": "John"}`)
	result, err := validator.ValidateAndApply(data, resolved)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify the result contains the default value
	var resultMap map[string]any
	if err := json.Unmarshal(result, &resultMap); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if resultMap["name"] != "John" {
		t.Errorf("Expected name to be 'John', got: %v", resultMap["name"])
	}
	if resultMap["status"] != "active" {
		t.Errorf("Expected status to be 'active' (default), got: %v", resultMap["status"])
	}
}

func TestReflectionValidator_ValidateAndApply_ValidationError(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a schema with required field
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer", "minimum": 0}
		},
		"required": ["name"]
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test data missing required field
	data := json.RawMessage(`{"age": 30}`)
	result, err := validator.ValidateAndApply(data, resolved)
	if err == nil {
		t.Fatalf("Expected validation error for missing required field, got result: %s", result)
	}

	var schemaErr *SchemaValidationError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("Expected SchemaValidationError, got: %T", err)
	}

	if schemaErr.Operation != "validation" {
		t.Errorf("Expected operation to be 'validation', got: %s", schemaErr.Operation)
	}
}

func TestReflectionValidator_ValidateAndApply_UnmarshalError(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a simple schema
	schemaJSON := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		}
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test invalid JSON data
	data := json.RawMessage(`{"name": "John", "invalid": }`)
	_, err = validator.ValidateAndApply(data, resolved)
	if err == nil {
		t.Fatal("Expected unmarshaling error for invalid JSON")
	}

	var schemaErr *SchemaValidationError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("Expected SchemaValidationError, got: %T", err)
	}

	if schemaErr.Operation != "unmarshaling" {
		t.Errorf("Expected operation to be 'unmarshaling', got: %s", schemaErr.Operation)
	}
}

func TestReflectionValidator_ValidateAndApply_EmptyData(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a schema with default values
	schemaJSON := `{
		"type": "object",
		"properties": {
			"status": {"type": "string", "default": "active"}
		}
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test with empty data
	data := json.RawMessage(``)
	result, err := validator.ValidateAndApply(data, resolved)
	if err != nil {
		t.Fatalf("Expected no error with empty data, got: %v", err)
	}

	// Verify the result contains the default value
	var resultMap map[string]any
	if err := json.Unmarshal(result, &resultMap); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if resultMap["status"] != "active" {
		t.Errorf("Expected status to be 'active' (default), got: %v", resultMap["status"])
	}
}

func TestReflectionValidator_ValidateAndApply_NestedObject(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a schema with nested objects
	schemaJSON := `{
		"type": "object",
		"properties": {
			"user": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"email": {"type": "string"}
				},
				"required": ["name"]
			}
		},
		"required": ["user"]
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test valid nested data
	data := json.RawMessage(`{"user": {"name": "John", "email": "john@example.com"}}`)
	result, err := validator.ValidateAndApply(data, resolved)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify the result structure
	var resultMap map[string]any
	if err := json.Unmarshal(result, &resultMap); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	user, ok := resultMap["user"].(map[string]any)
	if !ok {
		t.Fatalf("Expected user to be a map, got: %T", resultMap["user"])
	}

	if user["name"] != "John" {
		t.Errorf("Expected user name to be 'John', got: %v", user["name"])
	}
	if user["email"] != "john@example.com" {
		t.Errorf("Expected user email to be 'john@example.com', got: %v", user["email"])
	}
}

func TestReflectionValidator_ValidateAndApply_UnsupportedSchemaType(t *testing.T) {
	validator := NewReflectionValidator()

	// Create a schema with unsupported type
	schemaJSON := `{
		"type": "null"
	}`

	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		t.Fatalf("Failed to unmarshal schema: %v", err)
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Test with unsupported schema type
	data := json.RawMessage(`null`)
	_, err = validator.ValidateAndApply(data, resolved)
	if err == nil {
		t.Fatal("Expected error for unsupported schema type")
	}

	var schemaErr *SchemaValidationError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("Expected SchemaValidationError, got: %T", err)
	}

	if schemaErr.Operation != "schema_conversion" {
		t.Errorf("Expected operation to be 'schema_conversion', got: %s", schemaErr.Operation)
	}
}
