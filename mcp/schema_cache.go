// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"reflect"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// schemaCache provides concurrent-safe caching for JSON schemas.
// It caches both by reflect.Type (for auto-generated schemas) and
// by schema pointer (for pre-defined schemas).
//
// This cache significantly improves performance for stateless server deployments
// where tools are re-registered on every request. Without caching, each AddTool
// call would trigger expensive reflection-based schema generation and resolution.
//
// Create a cache using [NewSchemaCache] and pass it to [ServerOptions.SchemaCache].
type schemaCache struct {
	// byType caches schemas generated from Go types via jsonschema.ForType.
	// Key: reflect.Type, Value: *cachedSchema
	byType sync.Map

	// bySchema caches resolved schemas for pre-defined Schema objects.
	// Key: *jsonschema.Schema (pointer identity), Value: *jsonschema.Resolved
	// This uses pointer identity because integrators typically reuse the same
	// Tool objects across requests, so the schema pointer remains stable.
	bySchema sync.Map
}

// cachedSchema holds both the generated schema and its resolved form.
type cachedSchema struct {
	schema   *jsonschema.Schema
	resolved *jsonschema.Resolved
}

// NewSchemaCache creates a new schema cache for use with [ServerOptions.SchemaCache].
// Safe for concurrent use, unbounded.
func NewSchemaCache() *schemaCache {
	return &schemaCache{}
}

// getByType retrieves a cached schema by Go type.
// Returns the schema, resolved schema, and whether the cache hit.
func (c *schemaCache) getByType(t reflect.Type) (*jsonschema.Schema, *jsonschema.Resolved, bool) {
	if v, ok := c.byType.Load(t); ok {
		cs := v.(*cachedSchema)
		return cs.schema, cs.resolved, true
	}
	return nil, nil, false
}

// setByType caches a schema by Go type.
func (c *schemaCache) setByType(t reflect.Type, schema *jsonschema.Schema, resolved *jsonschema.Resolved) {
	c.byType.Store(t, &cachedSchema{schema: schema, resolved: resolved})
}

// getBySchema retrieves a cached resolved schema by the original schema pointer.
// This is used when integrators provide pre-defined schemas (e.g., github-mcp-server pattern).
func (c *schemaCache) getBySchema(schema *jsonschema.Schema) (*jsonschema.Resolved, bool) {
	if v, ok := c.bySchema.Load(schema); ok {
		return v.(*jsonschema.Resolved), true
	}
	return nil, false
}

// setBySchema caches a resolved schema by the original schema pointer.
func (c *schemaCache) setBySchema(schema *jsonschema.Schema, resolved *jsonschema.Resolved) {
	c.bySchema.Store(schema, resolved)
}
