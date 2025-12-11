// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestSchemaCacheByType(t *testing.T) {
	cache := NewSchemaCache()

	type TestInput struct {
		Name string `json:"name"`
	}

	rt := reflect.TypeFor[TestInput]()

	// Initially not in cache
	_, _, ok := cache.getByType(rt)
	if ok {
		t.Error("expected cache miss for new type")
	}

	// Add to cache
	schema := &jsonschema.Schema{Type: "object"}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("failed to resolve schema: %v", err)
	}
	cache.setByType(rt, schema, resolved)

	// Now should hit
	gotSchema, gotResolved, ok := cache.getByType(rt)
	if !ok {
		t.Error("expected cache hit after set")
	}
	if gotSchema != schema {
		t.Error("schema mismatch")
	}
	if gotResolved != resolved {
		t.Error("resolved schema mismatch")
	}
}

func TestSchemaCacheBySchema(t *testing.T) {
	cache := NewSchemaCache()

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"query": {Type: "string"},
		},
	}

	// Initially not in cache
	_, ok := cache.getBySchema(schema)
	if ok {
		t.Error("expected cache miss for new schema")
	}

	// Add to cache
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("failed to resolve schema: %v", err)
	}
	cache.setBySchema(schema, resolved)

	// Now should hit
	gotResolved, ok := cache.getBySchema(schema)
	if !ok {
		t.Error("expected cache hit after set")
	}
	if gotResolved != resolved {
		t.Error("resolved schema mismatch")
	}

	// Different schema pointer should miss
	schema2 := &jsonschema.Schema{Type: "object"}
	_, ok = cache.getBySchema(schema2)
	if ok {
		t.Error("expected cache miss for different schema pointer")
	}
}

func TestSetSchemaCachesGeneratedSchemas(t *testing.T) {
	cache := NewSchemaCache()

	type TestInput struct {
		Query string `json:"query"`
	}

	rt := reflect.TypeFor[TestInput]()

	// First call should generate and cache
	var sfield1 any
	var rfield1 *jsonschema.Resolved
	_, err := setSchema[TestInput](&sfield1, &rfield1, cache)
	if err != nil {
		t.Fatalf("setSchema failed: %v", err)
	}

	// Verify it's in cache
	cachedSchema, cachedResolved, ok := cache.getByType(rt)
	if !ok {
		t.Fatal("schema not cached after first setSchema call")
	}

	// Second call should hit cache
	var sfield2 any
	var rfield2 *jsonschema.Resolved
	_, err = setSchema[TestInput](&sfield2, &rfield2, cache)
	if err != nil {
		t.Fatalf("setSchema failed on second call: %v", err)
	}

	// Should return same cached objects
	if sfield2.(*jsonschema.Schema) != cachedSchema {
		t.Error("expected cached schema to be returned")
	}
	if rfield2 != cachedResolved {
		t.Error("expected cached resolved schema to be returned")
	}
}

func TestSetSchemaCachesProvidedSchemas(t *testing.T) {
	cache := NewSchemaCache()

	// This simulates the github-mcp-server pattern:
	// schema is created once and reused across requests
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"query": {Type: "string"},
		},
	}

	// First call should resolve and cache
	var sfield1 any = schema
	var rfield1 *jsonschema.Resolved
	_, err := setSchema[map[string]any](&sfield1, &rfield1, cache)
	if err != nil {
		t.Fatalf("setSchema failed: %v", err)
	}

	// Verify it's in cache
	cachedResolved, ok := cache.getBySchema(schema)
	if !ok {
		t.Fatal("resolved schema not cached after first setSchema call")
	}
	if rfield1 != cachedResolved {
		t.Error("expected same resolved schema")
	}

	// Second call with same schema pointer should hit cache
	var sfield2 any = schema
	var rfield2 *jsonschema.Resolved
	_, err = setSchema[map[string]any](&sfield2, &rfield2, cache)
	if err != nil {
		t.Fatalf("setSchema failed on second call: %v", err)
	}

	if rfield2 != cachedResolved {
		t.Error("expected cached resolved schema to be returned")
	}
}

func TestSetSchemaNoCacheWhenNil(t *testing.T) {
	type TestInput struct {
		Query string `json:"query"`
	}

	// First call without cache
	var sfield1 any
	var rfield1 *jsonschema.Resolved
	_, err := setSchema[TestInput](&sfield1, &rfield1, nil)
	if err != nil {
		t.Fatalf("setSchema failed: %v", err)
	}

	// Second call without cache - should still generate a new schema
	var sfield2 any
	var rfield2 *jsonschema.Resolved
	_, err = setSchema[TestInput](&sfield2, &rfield2, nil)
	if err != nil {
		t.Fatalf("setSchema failed on second call: %v", err)
	}

	// Both calls should succeed, schemas should be equivalent but not same pointer
	// (since no caching is happening)
	if sfield1 == nil || sfield2 == nil {
		t.Error("expected schemas to be generated")
	}
	if rfield1 == nil || rfield2 == nil {
		t.Error("expected resolved schemas to be generated")
	}
}

func TestAddToolCachesBetweenCalls(t *testing.T) {
	cache := NewSchemaCache()

	type GreetInput struct {
		Name string `json:"name" jsonschema:"the name to greet"`
	}

	type GreetOutput struct {
		Message string `json:"message"`
	}

	handler := func(ctx context.Context, req *CallToolRequest, in GreetInput) (*CallToolResult, GreetOutput, error) {
		return &CallToolResult{}, GreetOutput{Message: "Hello, " + in.Name}, nil
	}

	tool := &Tool{
		Name:        "greet",
		Description: "Greet someone",
	}

	// Simulate stateless server pattern: create new server each time, but share cache
	for i := 0; i < 3; i++ {
		s := NewServer(&Implementation{Name: "test", Version: "1.0"}, &ServerOptions{
			SchemaCache: cache,
		})
		AddTool(s, tool, handler)
	}

	// Verify schema was cached by type
	rt := reflect.TypeFor[GreetInput]()
	_, _, ok := cache.getByType(rt)
	if !ok {
		t.Error("expected schema to be cached by type after multiple AddTool calls")
	}
}
