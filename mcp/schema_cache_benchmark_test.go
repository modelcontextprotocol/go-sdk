// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// BenchmarkAddToolTypedHandler measures performance of AddTool with typed handlers.
// This simulates the stateless server pattern where new servers are created per request.
func BenchmarkAddToolTypedHandler(b *testing.B) {
	type SearchInput struct {
		Query   string `json:"query" jsonschema:"required"`
		Page    int    `json:"page"`
		PerPage int    `json:"per_page"`
	}

	type SearchOutput struct {
		Results []string `json:"results"`
		Total   int      `json:"total"`
	}

	handler := func(ctx context.Context, req *CallToolRequest, in SearchInput) (*CallToolResult, SearchOutput, error) {
		return &CallToolResult{}, SearchOutput{}, nil
	}

	tool := &Tool{
		Name:        "search",
		Description: "Search for items",
	}

	// Create a shared cache for caching benefit
	cache := NewSchemaCache()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s := NewServer(&Implementation{Name: "test", Version: "1.0"}, &ServerOptions{
			SchemaCache: cache,
		})
		AddTool(s, tool, handler)
	}
}

// BenchmarkAddToolPreDefinedSchema measures performance with pre-defined schemas.
// This simulates how github-mcp-server registers tools with manual InputSchema.
func BenchmarkAddToolPreDefinedSchema(b *testing.B) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"query":    {Type: "string", Description: "Search query"},
			"page":     {Type: "integer", Description: "Page number"},
			"per_page": {Type: "integer", Description: "Results per page"},
		},
		Required: []string{"query"},
	}

	handler := func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
		return &CallToolResult{}, nil
	}

	tool := &Tool{
		Name:        "search",
		Description: "Search for items",
		InputSchema: schema, // Pre-defined schema like github-mcp-server
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s := NewServer(&Implementation{Name: "test", Version: "1.0"}, nil)
		s.AddTool(tool, handler)
	}
}

// BenchmarkAddToolTypedHandlerNoCache measures performance without caching.
// Used to compare before/after performance.
func BenchmarkAddToolTypedHandlerNoCache(b *testing.B) {
	type SearchInput struct {
		Query   string `json:"query" jsonschema:"required"`
		Page    int    `json:"page"`
		PerPage int    `json:"per_page"`
	}

	type SearchOutput struct {
		Results []string `json:"results"`
		Total   int      `json:"total"`
	}

	handler := func(ctx context.Context, req *CallToolRequest, in SearchInput) (*CallToolResult, SearchOutput, error) {
		return &CallToolResult{}, SearchOutput{}, nil
	}

	tool := &Tool{
		Name:        "search",
		Description: "Search for items",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// No cache - each iteration generates new schemas
		s := NewServer(&Implementation{Name: "test", Version: "1.0"}, nil)
		AddTool(s, tool, handler)
	}
}

// BenchmarkAddToolMultipleTools simulates registering multiple tools like github-mcp-server.
func BenchmarkAddToolMultipleTools(b *testing.B) {
	type Input1 struct {
		Query string `json:"query"`
	}
	type Input2 struct {
		ID int `json:"id"`
	}
	type Input3 struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type Output struct {
		Success bool `json:"success"`
	}

	handler1 := func(ctx context.Context, req *CallToolRequest, in Input1) (*CallToolResult, Output, error) {
		return &CallToolResult{}, Output{}, nil
	}
	handler2 := func(ctx context.Context, req *CallToolRequest, in Input2) (*CallToolResult, Output, error) {
		return &CallToolResult{}, Output{}, nil
	}
	handler3 := func(ctx context.Context, req *CallToolRequest, in Input3) (*CallToolResult, Output, error) {
		return &CallToolResult{}, Output{}, nil
	}

	tool1 := &Tool{Name: "tool1", Description: "Tool 1"}
	tool2 := &Tool{Name: "tool2", Description: "Tool 2"}
	tool3 := &Tool{Name: "tool3", Description: "Tool 3"}

	// Create a shared cache for caching benefit
	cache := NewSchemaCache()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s := NewServer(&Implementation{Name: "test", Version: "1.0"}, &ServerOptions{
			SchemaCache: cache,
		})
		AddTool(s, tool1, handler1)
		AddTool(s, tool2, handler2)
		AddTool(s, tool3, handler3)
	}
}
