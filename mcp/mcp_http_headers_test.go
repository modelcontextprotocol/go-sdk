// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestExtractName(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		params   json.RawMessage
		wantName string
		wantOK   bool
	}{
		{
			name:     "tools/call",
			method:   "tools/call",
			params:   mustMarshal(&CallToolParams{Name: "my-tool"}),
			wantName: "my-tool",
			wantOK:   true,
		},
		{
			name:     "prompts/get",
			method:   "prompts/get",
			params:   mustMarshal(&GetPromptParams{Name: "code_review"}),
			wantName: "code_review",
			wantOK:   true,
		},
		{
			name:     "resources/read",
			method:   "resources/read",
			params:   mustMarshal(&ReadResourceParams{URI: "file:///info.txt"}),
			wantName: "file:///info.txt",
			wantOK:   true,
		},
		{
			name:     "tools/call with empty name",
			method:   "tools/call",
			params:   mustMarshal(&CallToolParams{Name: ""}),
			wantName: "",
			wantOK:   true,
		},
		{
			name:     "tool name with hyphen",
			method:   "tools/call",
			params:   mustMarshal(&CallToolParams{Name: "my-tool-v2"}),
			wantName: "my-tool-v2",
			wantOK:   true,
		},
		{
			name:     "tool name with underscore",
			method:   "tools/call",
			params:   mustMarshal(&CallToolParams{Name: "my_tool_name"}),
			wantName: "my_tool_name",
			wantOK:   true,
		},
		{
			name:     "resource URI with special chars",
			method:   "resources/read",
			params:   mustMarshal(&ReadResourceParams{URI: "file:///path/to/file%20name.txt"}),
			wantName: "file:///path/to/file%20name.txt",
			wantOK:   true,
		},
		{
			name:     "resource URI with query string",
			method:   "resources/read",
			params:   mustMarshal(&ReadResourceParams{URI: "https://example.com/resource?id=123"}),
			wantName: "https://example.com/resource?id=123",
			wantOK:   true,
		},
		{
			name:     "unrelated method",
			method:   "initialize",
			params:   mustMarshal(&InitializeParams{ProtocolVersion: "2025-06-18"}),
			wantName: "",
			wantOK:   false,
		},
		{
			name:     "notification method",
			method:   "notifications/initialized",
			params:   nil,
			wantName: "",
			wantOK:   false,
		},
		{
			name:     "invalid JSON params",
			method:   "tools/call",
			params:   []byte("not json"),
			wantName: "",
			wantOK:   false,
		},
		{
			name:     "nil params",
			method:   "tools/call",
			params:   nil,
			wantName: "",
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotOK := extractName(tt.method, tt.params)
			if gotName != tt.wantName || gotOK != tt.wantOK {
				t.Errorf("extractName(%q, ...) = (%q, %v), want (%q, %v)",
					tt.method, gotName, gotOK, tt.wantName, tt.wantOK)
			}
		})
	}
}

func TestSetStandardHeaders(t *testing.T) {
	tests := []struct {
		name             string
		protocolVersion  string
		msg              jsonrpc.Message
		wantMethodHeader string
		wantNameHeader   string
	}{
		{
			name:             "tools/call with future version",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantMethodHeader: "tools/call",
			wantNameHeader:   "my-tool",
		},
		{
			name:             "prompts/get with future version",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "code_review"})},
			wantMethodHeader: "prompts/get",
			wantNameHeader:   "code_review",
		},
		{
			name:             "resources/read with future version",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///info.txt"})},
			wantMethodHeader: "resources/read",
			wantNameHeader:   "file:///info.txt",
		},
		{
			name:             "initialize sets method only",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "initialize", Params: mustMarshal(&InitializeParams{ProtocolVersion: MinVersionForStandardHeaders})},
			wantMethodHeader: "initialize",
			wantNameHeader:   "",
		},
		{
			name:             "notification sets method only",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "notifications/initialized"},
			wantMethodHeader: "notifications/initialized",
			wantNameHeader:   "",
		},
		{
			name:             "old version skips all headers",
			protocolVersion:  protocolVersion20251125,
			msg:              &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantMethodHeader: "",
			wantNameHeader:   "",
		},
		{
			name:             "empty version skips all headers",
			protocolVersion:  "",
			msg:              &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantMethodHeader: "",
			wantNameHeader:   "",
		},
		{
			name:             "nil message is a no-op",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              nil,
			wantMethodHeader: "",
			wantNameHeader:   "",
		},
		{
			name:             "response message is ignored",
			protocolVersion:  MinVersionForStandardHeaders,
			msg:              &jsonrpc.Response{},
			wantMethodHeader: "",
			wantNameHeader:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := http.NewRequest("POST", "http://localhost/mcp", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.protocolVersion != "" {
				httpReq.Header.Set(ProtocolVersionHeader, tt.protocolVersion)
			}

			setStandardHeaders(httpReq, tt.msg)

			if got := httpReq.Header.Get(MethodHeader); got != tt.wantMethodHeader {
				t.Errorf("MethodHeader = %q, want %q", got, tt.wantMethodHeader)
			}
			if got := httpReq.Header.Get(NameHeader); got != tt.wantNameHeader {
				t.Errorf("NameHeader = %q, want %q", got, tt.wantNameHeader)
			}
		})
	}
}

func TestValidateMcpHeaders(t *testing.T) {
	futureVersion := MinVersionForStandardHeaders
	oldVersion := protocolVersion20251125

	tests := []struct {
		name           string
		version        string
		methodHeader   string
		nameHeader     string
		msg            jsonrpc.Message
		wantErr        bool
		wantErrContain string
	}{
		// -- Version gating --
		{
			name:    "old version skips validation",
			version: oldVersion,
			msg:     &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr: false,
		},
		{
			name:    "empty version skips validation",
			version: "",
			msg:     &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr: false,
		},

		// -- Missing headers --
		{
			name:           "missing Mcp-Method header",
			version:        futureVersion,
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Method header",
		},
		{
			name:           "missing Mcp-Name for tools/call",
			version:        futureVersion,
			methodHeader:   "tools/call",
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Name header",
		},
		{
			name:           "missing Mcp-Name for resources/read",
			version:        futureVersion,
			methodHeader:   "resources/read",
			msg:            &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///info.txt"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Name header",
		},
		{
			name:           "missing Mcp-Name for prompts/get",
			version:        futureVersion,
			methodHeader:   "prompts/get",
			msg:            &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "review"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Name header",
		},

		// -- Mismatches --
		{
			name:           "method mismatch",
			version:        futureVersion,
			methodHeader:   "tools/call",
			msg:            &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "review"})},
			wantErr:        true,
			wantErrContain: "Mcp-Method header value 'tools/call' does not match body value 'prompts/get'",
		},
		{
			name:           "tool name mismatch",
			version:        futureVersion,
			methodHeader:   "tools/call",
			nameHeader:     "wrong-tool",
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "right-tool"})},
			wantErr:        true,
			wantErrContain: "Mcp-Name header value 'wrong-tool' does not match body value 'right-tool'",
		},
		{
			name:           "resource URI mismatch",
			version:        futureVersion,
			methodHeader:   "resources/read",
			nameHeader:     "file:///wrong.txt",
			msg:            &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///right.txt"})},
			wantErr:        true,
			wantErrContain: "Mcp-Name header value 'file:///wrong.txt' does not match body value 'file:///right.txt'",
		},
		{
			name:           "prompt name mismatch",
			version:        futureVersion,
			methodHeader:   "prompts/get",
			nameHeader:     "wrong-prompt",
			msg:            &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "right-prompt"})},
			wantErr:        true,
			wantErrContain: "Mcp-Name header value 'wrong-prompt' does not match body value 'right-prompt'",
		},

		// -- Case sensitivity --
		{
			name:           "method value is case-sensitive",
			version:        futureVersion,
			methodHeader:   "TOOLS/CALL",
			nameHeader:     "my-tool",
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:        true,
			wantErrContain: "Mcp-Method header value 'TOOLS/CALL' does not match body value 'tools/call'",
		},

		// -- Valid cases --
		{
			name:         "valid tools/call",
			version:      futureVersion,
			methodHeader: "tools/call",
			nameHeader:   "my-tool",
			msg:          &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:      false,
		},
		{
			name:         "valid resources/read",
			version:      futureVersion,
			methodHeader: "resources/read",
			nameHeader:   "file:///info.txt",
			msg:          &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///info.txt"})},
			wantErr:      false,
		},
		{
			name:         "valid prompts/get",
			version:      futureVersion,
			methodHeader: "prompts/get",
			nameHeader:   "code_review",
			msg:          &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "code_review"})},
			wantErr:      false,
		},
		{
			name:         "valid initialize (no name needed)",
			version:      futureVersion,
			methodHeader: "initialize",
			msg:          &jsonrpc.Request{Method: "initialize", Params: mustMarshal(&InitializeParams{ProtocolVersion: MinVersionForStandardHeaders})},
			wantErr:      false,
		},
		{
			name:         "valid notification (no name needed)",
			version:      futureVersion,
			methodHeader: "notifications/initialized",
			msg:          &jsonrpc.Request{Method: "notifications/initialized"},
			wantErr:      false,
		},

		// -- Special characters --
		{
			name:         "tool name with hyphen",
			version:      futureVersion,
			methodHeader: "tools/call",
			nameHeader:   "my-tool-name",
			msg:          &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool-name"})},
			wantErr:      false,
		},
		{
			name:         "tool name with underscore",
			version:      futureVersion,
			methodHeader: "tools/call",
			nameHeader:   "my_tool_name",
			msg:          &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my_tool_name"})},
			wantErr:      false,
		},
		{
			name:         "resource URI with special chars",
			version:      futureVersion,
			methodHeader: "resources/read",
			nameHeader:   "file:///path/to/file%20name.txt",
			msg:          &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///path/to/file%20name.txt"})},
			wantErr:      false,
		},
		{
			name:         "resource URI with query string",
			version:      futureVersion,
			methodHeader: "resources/read",
			nameHeader:   "https://example.com/resource?id=123",
			msg:          &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "https://example.com/resource?id=123"})},
			wantErr:      false,
		},

		// -- Non-request messages --
		{
			name:    "response message is ignored",
			version: futureVersion,
			msg:     &jsonrpc.Response{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := http.NewRequest("POST", "http://localhost/mcp", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.version != "" {
				httpReq.Header.Set(ProtocolVersionHeader, tt.version)
			}
			if tt.methodHeader != "" {
				httpReq.Header.Set(MethodHeader, tt.methodHeader)
			}
			if tt.nameHeader != "" {
				httpReq.Header.Set(NameHeader, tt.nameHeader)
			}

			err = validateMcpHeaders(httpReq, tt.msg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("validateMcpHeaders() = nil, want error containing %q", tt.wantErrContain)
				}
				if !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("validateMcpHeaders() error = %q, want substring %q", err.Error(), tt.wantErrContain)
				}
			} else if err != nil {
				t.Errorf("validateMcpHeaders() = %v, want nil", err)
			}
		})
	}
}

func TestValidateToolParamHeaders(t *testing.T) {
	tests := []struct {
		name       string
		tool       *Tool
		wantErr    bool
		wantErrSub string
	}{
		{
			name: "valid tool with x-mcp-header",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":         "string",
							"x-mcp-header": "Region",
						},
					},
				},
			},
		},
		{
			name: "tool with no x-mcp-header annotations",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			},
		},
		{
			name: "empty x-mcp-header value",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":         "string",
							"x-mcp-header": "",
						},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "non-empty string",
		},
		{
			name: "x-mcp-header with space",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":         "string",
							"x-mcp-header": "My Region",
						},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "invalid character",
		},
		{
			name: "x-mcp-header with colon",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":         "string",
							"x-mcp-header": "Region:Primary",
						},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "invalid character",
		},
		{
			name: "x-mcp-header with non-ASCII",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region": map[string]any{
							"type":         "string",
							"x-mcp-header": "Région",
						},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "invalid character",
		},
		{
			name: "duplicate header names same case",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a": map[string]any{"type": "string", "x-mcp-header": "Region"},
						"b": map[string]any{"type": "string", "x-mcp-header": "Region"},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "duplicate",
		},
		{
			name: "duplicate header names different case",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"a": map[string]any{"type": "string", "x-mcp-header": "Region"},
						"b": map[string]any{"type": "string", "x-mcp-header": "REGION"},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "duplicate",
		},
		{
			name: "x-mcp-header on array type",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"items": map[string]any{
							"type":         "array",
							"x-mcp-header": "Items",
						},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "primitive types",
		},
		{
			name: "x-mcp-header on object type",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"nested": map[string]any{
							"type":         "object",
							"x-mcp-header": "Nested",
						},
					},
				},
			},
			wantErr:    true,
			wantErrSub: "primitive types",
		},
		{
			name: "x-mcp-header on number type is valid",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count": map[string]any{
							"type":         "number",
							"x-mcp-header": "Count",
						},
					},
				},
			},
		},
		{
			name: "x-mcp-header on integer type is valid",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count": map[string]any{
							"type":         "integer",
							"x-mcp-header": "Count",
						},
					},
				},
			},
		},
		{
			name: "x-mcp-header on boolean type is valid",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"flag": map[string]any{
							"type":         "boolean",
							"x-mcp-header": "Flag",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateToolParamHeaders(tt.tool)
			if tt.wantErr {
				if err == nil {
					t.Fatal("validateToolParamHeaders() = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErrSub)
				}
			} else if err != nil {
				t.Errorf("validateToolParamHeaders() = %v, want nil", err)
			}
		})
	}
}

func TestFilterValidTools(t *testing.T) {
	valid := &Tool{
		Name: "valid",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"region": map[string]any{"type": "string", "x-mcp-header": "Region"},
			},
		},
	}
	invalid := &Tool{
		Name: "invalid",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"region": map[string]any{"type": "string", "x-mcp-header": ""},
			},
		},
	}
	noAnnotation := &Tool{
		Name:        "plain",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
	}

	result := filterValidTools([]*Tool{valid, invalid, noAnnotation})
	if len(result) != 2 {
		t.Fatalf("filterValidTools returned %d tools, want 2", len(result))
	}
	if result[0].Name != "valid" || result[1].Name != "plain" {
		t.Errorf("filterValidTools returned [%s, %s], want [valid, plain]", result[0].Name, result[1].Name)
	}
}

func TestSetStandardHeadersWithParamHeaders(t *testing.T) {
	toolSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"region": map[string]any{
				"type":         "string",
				"x-mcp-header": "Region",
			},
			"query": map[string]any{
				"type": "string",
			},
			"priority": map[string]any{
				"type":         "string",
				"x-mcp-header": "Priority",
			},
		},
	}
	tool := &Tool{Name: "execute_sql", InputSchema: toolSchema}

	tests := []struct {
		name        string
		tool        *Tool
		params      any
		wantHeaders map[string]string
	}{
		{
			name: "sets param headers from arguments",
			tool: tool,
			params: &CallToolParams{
				Name:      "execute_sql",
				Arguments: map[string]any{"region": "us-west1", "query": "SELECT 1", "priority": "high"},
			},
			wantHeaders: map[string]string{
				"Mcp-Param-Region":   "us-west1",
				"Mcp-Param-Priority": "high",
			},
		},
		{
			name: "omits header when argument is missing",
			tool: tool,
			params: &CallToolParams{
				Name:      "execute_sql",
				Arguments: map[string]any{"query": "SELECT 1"},
			},
			wantHeaders: map[string]string{},
		},
		{
			name: "omits header when argument is null",
			tool: tool,
			params: &CallToolParams{
				Name:      "execute_sql",
				Arguments: map[string]any{"region": nil, "query": "SELECT 1"},
			},
			wantHeaders: map[string]string{},
		},
		{
			name: "encodes non-ASCII value",
			tool: tool,
			params: &CallToolParams{
				Name:      "execute_sql",
				Arguments: map[string]any{"region": "日本", "query": "SELECT 1"},
			},
			wantHeaders: map[string]string{
				"Mcp-Param-Region": "=?base64?5pel5pys?=",
			},
		},
		{
			name: "handles boolean argument",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"flag": map[string]any{"type": "boolean", "x-mcp-header": "Flag"},
					},
				},
			},
			params: &CallToolParams{
				Name:      "test",
				Arguments: map[string]any{"flag": true},
			},
			wantHeaders: map[string]string{
				"Mcp-Param-Flag": "true",
			},
		},
		{
			name: "handles number argument",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"count": map[string]any{"type": "number", "x-mcp-header": "Count"},
					},
				},
			},
			params: &CallToolParams{
				Name:      "test",
				Arguments: map[string]any{"count": float64(42)},
			},
			wantHeaders: map[string]string{
				"Mcp-Param-Count": "42",
			},
		},
		{
			name: "no tool in extra does not add param headers",
			tool: nil,
			params: &CallToolParams{
				Name:      "execute_sql",
				Arguments: map[string]any{"region": "us-west1"},
			},
			wantHeaders: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := http.NewRequest("POST", "http://localhost/mcp", nil)
			if err != nil {
				t.Fatal(err)
			}
			httpReq.Header.Set(ProtocolVersionHeader, MinVersionForStandardHeaders)

			msg := &jsonrpc.Request{
				Method: "tools/call",
				Params: mustMarshal(tt.params),
				Extra:  tt.tool,
			}

			setStandardHeaders(httpReq, msg)

			if got := httpReq.Header.Get(MethodHeader); got != "tools/call" {
				t.Errorf("MethodHeader = %q, want %q", got, "tools/call")
			}

			for header, want := range tt.wantHeaders {
				if got := httpReq.Header.Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}

			// Verify non-annotated params don't get headers
			if got := httpReq.Header.Get("Mcp-Param-query"); got != "" {
				t.Errorf("non-annotated param got header: Mcp-Param-query = %q", got)
			}
		})
	}
}

func TestExtractToolParamHeaders(t *testing.T) {
	tests := []struct {
		name string
		tool *Tool
		want map[string]string
	}{
		{
			name: "extracts x-mcp-header annotations",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"region":    map[string]any{"type": "string", "x-mcp-header": "Region"},
						"query":     map[string]any{"type": "string"},
						"tenant_id": map[string]any{"type": "string", "x-mcp-header": "TenantId"},
					},
				},
			},
			want: map[string]string{"region": "Region", "tenant_id": "TenantId"},
		},
		{
			name: "returns nil for tool without properties",
			tool: &Tool{Name: "test", InputSchema: map[string]any{"type": "object"}},
			want: nil,
		},
		{
			name: "returns nil for non-map schema",
			tool: &Tool{Name: "test", InputSchema: "not a map"},
			want: nil,
		},
		{
			name: "returns nil when no annotations",
			tool: &Tool{
				Name: "test",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"q": map[string]any{"type": "string"}},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractToolParamHeaders(tt.tool)
			if tt.want == nil {
				if got != nil {
					t.Errorf("extractToolParamHeaders() = %v, want nil", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("extractToolParamHeaders() returned %d entries, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("extractToolParamHeaders()[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestUnmarshalPrimitive(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want any
	}{
		{"string", `"hello"`, "hello"},
		{"number", `42`, float64(42)},
		{"float", `3.14`, float64(3.14)},
		{"true", `true`, true},
		{"false", `false`, false},
		{"null", `null`, nil},
		{"array", `[1,2]`, nil},
		{"object", `{"a":1}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unmarshalPrimitive(json.RawMessage(tt.raw))
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tt.want) {
				t.Errorf("unmarshalPrimitive(%s) = %v (%T), want %v (%T)", tt.raw, got, got, tt.want, tt.want)
			}
		})
	}
}
