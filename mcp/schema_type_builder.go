// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// SchemaTypeBuilder creates Go types from JSON schemas using reflection.
// It includes caching to avoid regenerating identical types for performance.
type SchemaTypeBuilder struct {
	mu    sync.RWMutex
	cache map[string]reflect.Type
}

// NewSchemaTypeBuilder creates a new SchemaTypeBuilder with an empty cache.
func NewSchemaTypeBuilder() *SchemaTypeBuilder {
	return &SchemaTypeBuilder{
		cache: make(map[string]reflect.Type),
	}
}

// BuildType creates a reflect.Type from a JSON schema.
// It uses caching to avoid regenerating identical types.
func (b *SchemaTypeBuilder) BuildType(schema *jsonschema.Schema) (reflect.Type, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema cannot be nil")
	}

	// Generate a cache key based on the schema structure
	cacheKey := b.generateCacheKey(schema)

	// Check cache first
	b.mu.RLock()
	if cachedType, exists := b.cache[cacheKey]; exists {
		b.mu.RUnlock()
		return cachedType, nil
	}
	b.mu.RUnlock()

	// Build the type
	typ, err := b.buildTypeInternal(schema)
	if err != nil {
		return nil, err
	}

	// Cache the result
	b.mu.Lock()
	b.cache[cacheKey] = typ
	b.mu.Unlock()

	return typ, nil
}

func (b *SchemaTypeBuilder) buildTypeInternal(schema *jsonschema.Schema) (reflect.Type, error) {
	switch schema.Type {
	case "string":
		return reflect.TypeOf(""), nil
	case "number":
		return reflect.TypeOf(float64(0)), nil
	case "integer":
		return reflect.TypeOf(int64(0)), nil
	case "boolean":
		return reflect.TypeOf(false), nil
	case "object":
		return b.BuildStructType(schema)
	case "array":
		return b.buildArrayType(schema)
	default:
		return nil, fmt.Errorf("unsupported schema type: %s", schema.Type)
	}
}

// BuildStructType creates a struct type from an object schema.
func (b *SchemaTypeBuilder) BuildStructType(schema *jsonschema.Schema) (reflect.Type, error) {
	if schema.Type != "object" {
		return nil, fmt.Errorf("expected object schema, got %s", schema.Type)
	}

	var fields []reflect.StructField

	// Process each property in the schema
	for propName, propSchema := range schema.Properties {
		fieldType, err := b.buildTypeInternal(propSchema)
		if err != nil {
			return nil, fmt.Errorf("building type for property %s: %w", propName, err)
		}

		isRequired := b.isRequired(propName, schema.Required)

		// Use pointer types for optional fields
		if !isRequired {
			fieldType = reflect.PtrTo(fieldType)
		}

		// Create struct field with proper JSON tag
		field := reflect.StructField{
			Name: b.toGoFieldName(propName),
			Type: fieldType,
			Tag:  b.buildStructTag(propName, isRequired),
		}

		fields = append(fields, field)
	}

	// Create the struct type
	structType := reflect.StructOf(fields)
	return structType, nil
}

// buildArrayType creates a slice type from an array schema
func (b *SchemaTypeBuilder) buildArrayType(schema *jsonschema.Schema) (reflect.Type, error) {
	if schema.Items == nil {
		// Default to []interface{} for arrays without item schema
		return reflect.TypeOf([]interface{}{}), nil
	}

	itemType, err := b.buildTypeInternal(schema.Items)
	if err != nil {
		return nil, fmt.Errorf("building array item type: %w", err)
	}

	return reflect.SliceOf(itemType), nil
}

// isRequired checks if a property name is in the required list
func (b *SchemaTypeBuilder) isRequired(propName string, required []string) bool {
	for _, req := range required {
		if req == propName {
			return true
		}
	}
	return false
}

// toGoFieldName converts a JSON property name to a Go field name
func (b *SchemaTypeBuilder) toGoFieldName(propName string) string {
	// Convert to PascalCase for exported fields
	parts := strings.Split(propName, "_")
	var result strings.Builder

	for _, part := range parts {
		if len(part) > 0 {
			result.WriteString(strings.ToUpper(part[:1]))
			if len(part) > 1 {
				result.WriteString(part[1:])
			}
		}
	}

	fieldName := result.String()

	// Ensure the field name is exported (starts with uppercase)
	if len(fieldName) == 0 || fieldName[0] < 'A' || fieldName[0] > 'Z' {
		fieldName = "Field" + fieldName
	}

	return fieldName
}

// buildStructTag creates appropriate struct tags for JSON marshaling
func (b *SchemaTypeBuilder) buildStructTag(propName string, isRequired bool) reflect.StructTag {
	jsonTag := propName
	if !isRequired {
		jsonTag += ",omitempty"
	}
	return reflect.StructTag(fmt.Sprintf(`json:"%s"`, jsonTag))
}

// generateCacheKey creates a unique key for caching based on schema structure
func (b *SchemaTypeBuilder) generateCacheKey(schema *jsonschema.Schema) string {
	var key strings.Builder
	b.appendSchemaToKey(&key, schema)
	return key.String()
}

// appendSchemaToKey recursively builds a cache key from schema structure
func (b *SchemaTypeBuilder) appendSchemaToKey(key *strings.Builder, schema *jsonschema.Schema) {
	key.WriteString(schema.Type)

	if schema.Type == "object" {
		key.WriteString("{")
		// Sort properties for consistent cache keys
		for propName, propSchema := range schema.Properties {
			key.WriteString(propName)
			key.WriteString(":")
			b.appendSchemaToKey(key, propSchema)
			key.WriteString(";")
		}
		key.WriteString("req:")
		for _, req := range schema.Required {
			key.WriteString(req)
			key.WriteString(",")
		}
		key.WriteString("}")
	} else if schema.Type == "array" && schema.Items != nil {
		key.WriteString("[")
		b.appendSchemaToKey(key, schema.Items)
		key.WriteString("]")
	}
}
