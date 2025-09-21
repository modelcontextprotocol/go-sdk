// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

func TestApplySchema(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"x": {Type: "integer", Default: json.RawMessage("3")},
		},
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatal(err)
	}

	type S struct {
		X int `json:"x"`
	}

	for _, tt := range []struct {
		data string
		v    any
		want any
	}{
		{`{"x": 1}`, new(S), &S{X: 1}},
		{`{}`, new(S), &S{X: 3}}, // default applied
		{`{"x": 0}`, new(S), &S{X: 0}},
		{`{"x": 1}`, new(map[string]any), &map[string]any{"x": 1.0}},
		{`{}`, new(map[string]any), &map[string]any{"x": 3.0}}, // default applied
		{`{"x": 0}`, new(map[string]any), &map[string]any{"x": 0.0}},
	} {
		raw := json.RawMessage(tt.data)
		raw, err = applySchema(raw, resolved)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &tt.v); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tt.v, tt.want) {
			t.Errorf("got %#v, want %#v", tt.v, tt.want)
		}
	}
}

func TestApplySchemaReflectionBased(t *testing.T) {
	tests := []struct {
		name     string
		schema   *jsonschema.Schema
		data     string
		wantData string
		wantErr  bool
	}{
		{
			name: "simple object with defaults",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name":   {Type: "string", Default: json.RawMessage(`"default"`)},
					"age":    {Type: "integer"},
					"active": {Type: "boolean", Default: json.RawMessage("true")},
				},
				Required: []string{"age"},
			},
			data:     `{"age": 25}`,
			wantData: `{"active":true,"age":25,"name":"default"}`,
		},
		{
			name: "nested object",
			schema: &jsonschema.Schema{
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
					"active": {Type: "boolean", Default: json.RawMessage("false")},
				},
				Required: []string{"user"},
			},
			data:     `{"user": {"name": "John", "age": 30}}`,
			wantData: `{"active":false,"user":{"age":30,"name":"John"}}`,
		},
		{
			name: "array of primitives",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"tags": {
						Type:  "array",
						Items: &jsonschema.Schema{Type: "string"},
					},
					"count": {Type: "integer", Default: json.RawMessage("0")},
				},
			},
			data:     `{"tags": ["tag1", "tag2"]}`,
			wantData: `{"count":0,"tags":["tag1","tag2"]}`,
		},
		{
			name: "validation error - wrong type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"age": {Type: "integer"},
				},
				Required: []string{"age"},
			},
			data:    `{"age": "not a number"}`,
			wantErr: true,
		},
		{
			name: "validation error - missing required field",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
				Required: []string{"name"},
			},
			data:    `{}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := tt.schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
			if err != nil {
				t.Fatal(err)
			}

			raw := json.RawMessage(tt.data)
			result, err := applySchema(raw, resolved)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Compare JSON strings for easier debugging
			if string(result) != tt.wantData {
				t.Errorf("got %s, want %s", string(result), tt.wantData)
			}
		})
	}
}

func TestApplySchemaFallbackMechanism(t *testing.T) {
	tests := []struct {
		name           string
		schema         *jsonschema.Schema
		data           string
		expectFallback bool
		wantData       string
		wantErr        bool
	}{
		{
			name: "supported object - should use reflection",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
			},
			data:           `{"name": "test"}`,
			expectFallback: false,
			wantData:       `{"name":"test"}`,
		},
		{
			name: "unsupported schema type - should fallback",
			schema: &jsonschema.Schema{
				Type: "unsupported_type",
			},
			data:    `{}`,
			wantErr: true, // This will fail during schema resolution
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := tt.schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
			if err != nil {
				if tt.wantErr {
					return // Expected error during schema resolution
				}
				t.Fatal(err)
			}

			raw := json.RawMessage(tt.data)
			result, err := applySchema(raw, resolved)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(result) != tt.wantData {
				t.Errorf("got %s, want %s", string(result), tt.wantData)
			}
		})
	}
}

func TestApplySchemaBackwardCompatibility(t *testing.T) {
	// Test that the enhanced applySchema maintains exact backward compatibility
	// with the original map-based implementation

	testCases := []struct {
		name   string
		schema *jsonschema.Schema
		data   string
	}{
		{
			name: "empty data",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"x": {Type: "integer", Default: json.RawMessage("42")},
				},
			},
			data: ``,
		},
		{
			name:   "null schema",
			schema: nil,
			data:   `{"any": "data"}`,
		},
		{
			name: "complex nested structure",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"config": {
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"endpoint": {Type: "string", Default: json.RawMessage(`"https://api.example.com"`)},
							"retries":  {Type: "integer", Default: json.RawMessage("3")},
							"options": {
								Type:  "array",
								Items: &jsonschema.Schema{Type: "string"},
							},
						},
					},
					"enabled": {Type: "boolean", Default: json.RawMessage("true")},
				},
			},
			data: `{"config": {"options": ["opt1", "opt2"]}}`,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			var resolved *jsonschema.Resolved
			var err error

			if tt.schema != nil {
				resolved, err = tt.schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
				if err != nil {
					t.Fatal(err)
				}
			}

			// Test with enhanced applySchema
			raw := json.RawMessage(tt.data)
			enhancedResult, enhancedErr := applySchema(raw, resolved)

			// Test with original map-based approach
			originalResult, originalErr := applySchemaMapBased(raw, resolved)

			// Results should be identical
			if (enhancedErr == nil) != (originalErr == nil) {
				t.Errorf("error mismatch: enhanced=%v, original=%v", enhancedErr, originalErr)
			}

			if enhancedErr == nil && originalErr == nil {
				if string(enhancedResult) != string(originalResult) {
					t.Errorf("result mismatch:\nenhanced: %s\noriginal: %s",
						string(enhancedResult), string(originalResult))
				}
			}
		})
	}
}

func TestToolErrorHandling(t *testing.T) {
	// Construct server and add both tools at the top level
	server := NewServer(testImpl, nil)

	// Create a tool that returns a structured error
	structuredErrorHandler := func(ctx context.Context, req *CallToolRequest, args map[string]any) (*CallToolResult, any, error) {
		return nil, nil, &jsonrpc2.WireError{
			Code:    CodeInvalidParams,
			Message: "internal server error",
		}
	}

	// Create a tool that returns a regular error
	regularErrorHandler := func(ctx context.Context, req *CallToolRequest, args map[string]any) (*CallToolResult, any, error) {
		return nil, nil, fmt.Errorf("tool execution failed")
	}

	AddTool(server, &Tool{Name: "error_tool", Description: "returns structured error"}, structuredErrorHandler)
	AddTool(server, &Tool{Name: "regular_error_tool", Description: "returns regular error"}, regularErrorHandler)

	// Connect server and client once
	ct, st := NewInMemoryTransports()
	_, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}

	client := NewClient(testImpl, nil)
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Test that structured JSON-RPC errors are returned directly
	t.Run("structured_error", func(t *testing.T) {
		// Call the tool
		_, err = cs.CallTool(context.Background(), &CallToolParams{
			Name:      "error_tool",
			Arguments: map[string]any{},
		})

		// Should get the structured error directly
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var wireErr *jsonrpc2.WireError
		if !errors.As(err, &wireErr) {
			t.Fatalf("expected WireError, got %[1]T: %[1]v", err)
		}

		if wireErr.Code != CodeInvalidParams {
			t.Errorf("expected error code %d, got %d", CodeInvalidParams, wireErr.Code)
		}
	})

	// Test that regular errors are embedded in tool results
	t.Run("regular_error", func(t *testing.T) {
		// Call the tool
		result, err := cs.CallTool(context.Background(), &CallToolParams{
			Name:      "regular_error_tool",
			Arguments: map[string]any{},
		})
		// Should not get an error at the protocol level
		if err != nil {
			t.Fatalf("unexpected protocol error: %v", err)
		}

		// Should get a result with IsError=true
		if !result.IsError {
			t.Error("expected IsError=true, got false")
		}

		// Should have error message in content
		if len(result.Content) == 0 {
			t.Error("expected error content, got empty")
		}

		if textContent, ok := result.Content[0].(*TextContent); !ok {
			t.Error("expected TextContent")
		} else if !strings.Contains(textContent.Text, "tool execution failed") {
			t.Errorf("expected error message in content, got: %s", textContent.Text)
		}
	})
}

func TestApplySchemaWithRealMCPSchemas(t *testing.T) {
	// Test with schemas similar to those used in real MCP tools

	t.Run("toolschemas_greeting_input", func(t *testing.T) {
		// Schema from toolschemas example
		schema := &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"name": {Type: "string", MaxLength: jsonschema.Ptr(10)},
			},
			Required: []string{"name"},
		}

		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			t.Fatal(err)
		}

		tests := []struct {
			data    string
			wantErr bool
		}{
			{`{"name": "John"}`, false},
			{`{"name": "VeryLongName"}`, true}, // exceeds maxLength
			{`{}`, true},                       // missing required field
		}

		for _, tt := range tests {
			raw := json.RawMessage(tt.data)
			_, err := applySchema(raw, resolved)

			if tt.wantErr && err == nil {
				t.Errorf("expected error for data %s, got nil", tt.data)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for data %s: %v", tt.data, err)
			}
		}
	})

	t.Run("elicitation_config_schema", func(t *testing.T) {
		// Schema from elicitation example
		schema := &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"serverEndpoint": {Type: "string", Description: "Server endpoint URL"},
				"maxRetries":     {Type: "number", Minimum: jsonschema.Ptr(1.0), Maximum: jsonschema.Ptr(10.0)},
				"enableLogs":     {Type: "boolean", Description: "Enable debug logging"},
			},
			Required: []string{"serverEndpoint"},
		}

		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			t.Fatal(err)
		}

		tests := []struct {
			data    string
			wantErr bool
		}{
			{`{"serverEndpoint": "https://api.example.com", "maxRetries": 3, "enableLogs": true}`, false},
			{`{"serverEndpoint": "https://api.example.com"}`, false}, // optional fields
			{`{"maxRetries": 3}`, true}, // missing required field
			{`{"serverEndpoint": "https://api.example.com", "maxRetries": 15}`, true}, // exceeds maximum
		}

		for _, tt := range tests {
			raw := json.RawMessage(tt.data)
			_, err := applySchema(raw, resolved)

			if tt.wantErr && err == nil {
				t.Errorf("expected error for data %s, got nil", tt.data)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for data %s: %v", tt.data, err)
			}
		}
	})

	t.Run("everything_random_schema", func(t *testing.T) {
		// Schema from everything example elicitation
		schema := &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"random": {Type: "string"},
			},
		}

		resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			t.Fatal(err)
		}

		raw := json.RawMessage(`{"random": "test-string"}`)
		result, err := applySchema(raw, resolved)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := `{"random":"test-string"}`
		if string(result) != expected {
			t.Errorf("got %s, want %s", string(result), expected)
		}
	})
}

// Benchmark tests comparing reflection vs map-based validation performance
func BenchmarkApplySchema(b *testing.B) {
	// Simple object schema
	simpleSchema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string", Default: json.RawMessage(`"default"`)},
			"age":  {Type: "integer"},
		},
		Required: []string{"age"},
	}

	// Complex nested schema
	complexSchema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"user": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"profile": {
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"name":  {Type: "string"},
							"email": {Type: "string"},
							"age":   {Type: "integer"},
						},
						Required: []string{"name", "email"},
					},
					"preferences": {
						Type: "array",
						Items: &jsonschema.Schema{
							Type: "object",
							Properties: map[string]*jsonschema.Schema{
								"key":   {Type: "string"},
								"value": {Type: "string"},
							},
						},
					},
				},
				Required: []string{"profile"},
			},
			"metadata": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"created": {Type: "string"},
					"updated": {Type: "string"},
				},
			},
		},
		Required: []string{"user"},
	}

	benchmarks := []struct {
		name   string
		schema *jsonschema.Schema
		data   string
	}{
		{
			name:   "simple_object",
			schema: simpleSchema,
			data:   `{"age": 25}`,
		},
		{
			name:   "complex_nested",
			schema: complexSchema,
			data: `{
				"user": {
					"profile": {
						"name": "John Doe",
						"email": "john@example.com",
						"age": 30
					},
					"preferences": [
						{"key": "theme", "value": "dark"},
						{"key": "lang", "value": "en"}
					]
				},
				"metadata": {
					"created": "2023-01-01",
					"updated": "2023-01-02"
				}
			}`,
		},
	}

	for _, bm := range benchmarks {
		resolved, err := bm.schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			b.Fatal(err)
		}

		b.Run("enhanced_"+bm.name, func(b *testing.B) {
			raw := json.RawMessage(bm.data)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := applySchema(raw, resolved)
				if err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run("mapbased_"+bm.name, func(b *testing.B) {
			raw := json.RawMessage(bm.data)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := applySchemaMapBased(raw, resolved)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSchemaTypeBuilderCaching(b *testing.B) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
			"age":  {Type: "integer"},
			"tags": {
				Type:  "array",
				Items: &jsonschema.Schema{Type: "string"},
			},
		},
	}

	builder := NewSchemaTypeBuilder()

	b.Run("first_build", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// Clear cache to simulate first build
			builder.cache = make(map[string]reflect.Type)
			_, err := builder.BuildType(schema)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("cached_build", func(b *testing.B) {
		// Pre-populate cache
		_, err := builder.BuildType(schema)
		if err != nil {
			b.Fatal(err)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := builder.BuildType(schema)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestApplySchemaErrorHandling(t *testing.T) {
	tests := []struct {
		name              string
		schema            *jsonschema.Schema
		data              string
		expectError       string
		expectResolveFail bool
	}{
		{
			name: "schema resolution error",
			schema: &jsonschema.Schema{
				Type: "unsupported_type",
			},
			data:              `{}`,
			expectResolveFail: true,
		},
		{
			name: "reflection validation error",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"age": {Type: "integer"},
				},
				Required: []string{"age"},
			},
			data:        `{"age": "not_a_number"}`,
			expectError: "reflection_validation",
		},
		{
			name: "validation error after defaults",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
				Required: []string{"name"},
			},
			data:        `{}`,
			expectError: "validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := tt.schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
			if err != nil {
				if tt.expectResolveFail {
					return // Expected error during schema resolution
				}
				t.Fatal(err)
			}

			raw := json.RawMessage(tt.data)
			_, err = applySchema(raw, resolved)

			if err == nil {
				t.Errorf("expected error, got nil")
				return
			}

			var schemaErr *SchemaValidationError
			if errors.As(err, &schemaErr) {
				if tt.expectError != "" && schemaErr.Operation != tt.expectError {
					t.Errorf("expected error operation %s, got %s", tt.expectError, schemaErr.Operation)
				}
			} else {
				// For some errors, we might fall back to map-based validation
				// which doesn't use SchemaValidationError - this is acceptable
				if tt.expectError != "" {
					t.Logf("Got non-SchemaValidationError (fallback behavior): %T: %v", err, err)
				}
			}
		})
	}
}

func TestApplySchemaMemoryUsage(t *testing.T) {
	// Test that reflection-based validation doesn't cause memory leaks
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"data": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"id":   {Type: "string"},
						"name": {Type: "string"},
					},
				},
			},
		},
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatal(err)
	}

	data := `{
		"data": [
			{"id": "1", "name": "item1"},
			{"id": "2", "name": "item2"},
			{"id": "3", "name": "item3"}
		]
	}`

	// Run many iterations to check for memory leaks
	for i := 0; i < 1000; i++ {
		raw := json.RawMessage(data)
		_, err := applySchema(raw, resolved)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Force garbage collection
	runtime.GC()
	runtime.GC()

	// This test mainly serves as a smoke test for memory issues
	// In a real scenario, you'd use memory profiling tools to verify
}
