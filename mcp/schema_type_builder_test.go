// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestSchemaTypeBuilder_BuildType_BasicTypes(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	tests := []struct {
		name       string
		schema     *jsonschema.Schema
		expectType reflect.Type
	}{
		{
			name:       "string type",
			schema:     &jsonschema.Schema{Type: "string"},
			expectType: reflect.TypeOf(""),
		},
		{
			name:       "number type",
			schema:     &jsonschema.Schema{Type: "number"},
			expectType: reflect.TypeOf(float64(0)),
		},
		{
			name:       "integer type",
			schema:     &jsonschema.Schema{Type: "integer"},
			expectType: reflect.TypeOf(int64(0)),
		},
		{
			name:       "boolean type",
			schema:     &jsonschema.Schema{Type: "boolean"},
			expectType: reflect.TypeOf(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, err := builder.BuildType(tt.schema)
			if err != nil {
				t.Fatalf("BuildType() error = %v", err)
			}
			if typ != tt.expectType {
				t.Errorf("BuildType() = %v, want %v", typ, tt.expectType)
			}
		})
	}
}

func TestSchemaTypeBuilder_BuildType_Arrays(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	tests := []struct {
		name       string
		schema     *jsonschema.Schema
		expectType reflect.Type
	}{
		{
			name: "string array",
			schema: &jsonschema.Schema{
				Type:  "array",
				Items: &jsonschema.Schema{Type: "string"},
			},
			expectType: reflect.TypeOf([]string{}),
		},
		{
			name: "number array",
			schema: &jsonschema.Schema{
				Type:  "array",
				Items: &jsonschema.Schema{Type: "number"},
			},
			expectType: reflect.TypeOf([]float64{}),
		},
		{
			name: "array without items schema",
			schema: &jsonschema.Schema{
				Type: "array",
			},
			expectType: reflect.TypeOf([]interface{}{}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, err := builder.BuildType(tt.schema)
			if err != nil {
				t.Fatalf("BuildType() error = %v", err)
			}
			if typ != tt.expectType {
				t.Errorf("BuildType() = %v, want %v", typ, tt.expectType)
			}
		})
	}
}

func TestSchemaTypeBuilder_BuildStructType(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	// Test simple object with required and optional fields
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name":  {Type: "string"},
			"age":   {Type: "integer"},
			"email": {Type: "string"},
		},
		Required: []string{"name"},
	}

	typ, err := builder.BuildType(schema)
	if err != nil {
		t.Fatalf("BuildType() error = %v", err)
	}

	if typ.Kind() != reflect.Struct {
		t.Fatalf("Expected struct type, got %v", typ.Kind())
	}

	// Check number of fields
	if typ.NumField() != 3 {
		t.Fatalf("Expected 3 fields, got %d", typ.NumField())
	}

	// Check field types and tags
	nameField, found := typ.FieldByName("Name")
	if !found {
		t.Fatal("Name field not found")
	}
	if nameField.Type != reflect.TypeOf("") {
		t.Errorf("Name field type = %v, want %v", nameField.Type, reflect.TypeOf(""))
	}
	if nameField.Tag.Get("json") != "name" {
		t.Errorf("Name field JSON tag = %v, want 'name'", nameField.Tag.Get("json"))
	}

	ageField, found := typ.FieldByName("Age")
	if !found {
		t.Fatal("Age field not found")
	}
	if ageField.Type != reflect.PtrTo(reflect.TypeOf(int64(0))) {
		t.Errorf("Age field type = %v, want %v", ageField.Type, reflect.PtrTo(reflect.TypeOf(int64(0))))
	}
	if ageField.Tag.Get("json") != "age,omitempty" {
		t.Errorf("Age field JSON tag = %v, want 'age,omitempty'", ageField.Tag.Get("json"))
	}

	emailField, found := typ.FieldByName("Email")
	if !found {
		t.Fatal("Email field not found")
	}
	if emailField.Type != reflect.PtrTo(reflect.TypeOf("")) {
		t.Errorf("Email field type = %v, want %v", emailField.Type, reflect.PtrTo(reflect.TypeOf("")))
	}
	if emailField.Tag.Get("json") != "email,omitempty" {
		t.Errorf("Email field JSON tag = %v, want 'email,omitempty'", emailField.Tag.Get("json"))
	}
}

func TestSchemaTypeBuilder_BuildStructType_NestedObjects(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	// Test nested object
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"user": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
					"age":  {Type: "integer"},
				},
				Required: []string{"name"},
			},
			"active": {Type: "boolean"},
		},
		Required: []string{"user"},
	}

	typ, err := builder.BuildType(schema)
	if err != nil {
		t.Fatalf("BuildType() error = %v", err)
	}

	if typ.Kind() != reflect.Struct {
		t.Fatalf("Expected struct type, got %v", typ.Kind())
	}

	// Check that User field exists and is a struct
	userField, found := typ.FieldByName("User")
	if !found {
		t.Fatal("User field not found")
	}
	if userField.Type.Kind() != reflect.Struct {
		t.Errorf("User field type = %v, want struct", userField.Type.Kind())
	}

	// Check nested struct fields
	userType := userField.Type
	nameField, found := userType.FieldByName("Name")
	if !found {
		t.Fatal("Name field not found in nested struct")
	}
	if nameField.Type != reflect.TypeOf("") {
		t.Errorf("Name field type = %v, want %v", nameField.Type, reflect.TypeOf(""))
	}

	ageField, found := userType.FieldByName("Age")
	if !found {
		t.Fatal("Age field not found in nested struct")
	}
	if ageField.Type != reflect.PtrTo(reflect.TypeOf(int64(0))) {
		t.Errorf("Age field type = %v, want %v", ageField.Type, reflect.PtrTo(reflect.TypeOf(int64(0))))
	}
}

func TestSchemaTypeBuilder_Caching(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
			"age":  {Type: "integer"},
		},
		Required: []string{"name"},
	}

	// Build type first time
	typ1, err := builder.BuildType(schema)
	if err != nil {
		t.Fatalf("BuildType() error = %v", err)
	}

	// Build same type again
	typ2, err := builder.BuildType(schema)
	if err != nil {
		t.Fatalf("BuildType() error = %v", err)
	}

	// Should return the same type instance (cached)
	if typ1 != typ2 {
		t.Errorf("Expected cached type to be identical, got different instances")
	}

	// Verify cache contains the entry
	cacheKey := builder.generateCacheKey(schema)
	builder.mu.RLock()
	cachedType, exists := builder.cache[cacheKey]
	builder.mu.RUnlock()

	if !exists {
		t.Error("Expected type to be cached")
	}

	// For dynamically created types, we should check that they're the same instance
	// by comparing their string representation and ensuring they're the exact same pointer
	if cachedType.String() != typ1.String() {
		t.Errorf("Cached type string doesn't match: cached=%s, returned=%s", cachedType.String(), typ1.String())
	}

	// The most important test: verify that subsequent calls return the cached instance
	if typ1 != typ2 {
		t.Error("Second call should return cached instance")
	}
}

func TestSchemaTypeBuilder_CacheKeyGeneration(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	schema1 := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
			"age":  {Type: "integer"},
		},
		Required: []string{"name"},
	}

	schema2 := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
			"age":  {Type: "integer"},
		},
		Required: []string{"name"},
	}

	schema3 := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
			"age":  {Type: "integer"},
		},
		Required: []string{"name", "age"}, // Different required fields
	}

	key1 := builder.generateCacheKey(schema1)
	key2 := builder.generateCacheKey(schema2)
	key3 := builder.generateCacheKey(schema3)

	// Same schemas should generate same keys
	if key1 != key2 {
		t.Errorf("Expected same cache keys for identical schemas, got %s != %s", key1, key2)
	}

	// Different schemas should generate different keys
	if key1 == key3 {
		t.Errorf("Expected different cache keys for different schemas, got %s == %s", key1, key3)
	}
}

func TestSchemaTypeBuilder_ToGoFieldName(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	tests := []struct {
		input    string
		expected string
	}{
		{"name", "Name"},
		{"first_name", "FirstName"},
		{"user_id", "UserId"},
		{"", "Field"},
		{"a", "A"},
		{"camelCase", "CamelCase"},
		{"snake_case_field", "SnakeCaseField"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := builder.toGoFieldName(tt.input)
			if result != tt.expected {
				t.Errorf("toGoFieldName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSchemaTypeBuilder_ErrorCases(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	tests := []struct {
		name   string
		schema *jsonschema.Schema
	}{
		{
			name:   "nil schema",
			schema: nil,
		},
		{
			name:   "unsupported type",
			schema: &jsonschema.Schema{Type: "unsupported"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := builder.BuildType(tt.schema)
			if err == nil {
				t.Errorf("Expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestSchemaTypeBuilder_BuildStructType_ErrorCases(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	// Test non-object schema
	schema := &jsonschema.Schema{Type: "string"}
	_, err := builder.BuildStructType(schema)
	if err == nil {
		t.Error("Expected error for non-object schema")
	}
}

func TestSchemaTypeBuilder_ComplexSchema(t *testing.T) {
	builder := NewSchemaTypeBuilder()

	// Test a more complex schema with arrays and nested objects
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"users": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"name":  {Type: "string"},
						"email": {Type: "string"},
					},
					Required: []string{"name"},
				},
			},
			"metadata": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"version": {Type: "string"},
					"count":   {Type: "integer"},
				},
			},
		},
		Required: []string{"users"},
	}

	typ, err := builder.BuildType(schema)
	if err != nil {
		t.Fatalf("BuildType() error = %v", err)
	}

	if typ.Kind() != reflect.Struct {
		t.Fatalf("Expected struct type, got %v", typ.Kind())
	}

	// Check Users field (required array)
	usersField, found := typ.FieldByName("Users")
	if !found {
		t.Fatal("Users field not found")
	}
	if usersField.Type.Kind() != reflect.Slice {
		t.Errorf("Users field should be slice, got %v", usersField.Type.Kind())
	}

	// Check Metadata field (optional object)
	metadataField, found := typ.FieldByName("Metadata")
	if !found {
		t.Fatal("Metadata field not found")
	}
	if metadataField.Type.Kind() != reflect.Ptr {
		t.Errorf("Metadata field should be pointer (optional), got %v", metadataField.Type.Kind())
	}
	if metadataField.Type.Elem().Kind() != reflect.Struct {
		t.Errorf("Metadata field should point to struct, got %v", metadataField.Type.Elem().Kind())
	}
}
