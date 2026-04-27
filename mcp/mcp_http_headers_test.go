// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
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
			protocolVersion:  minVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantMethodHeader: "tools/call",
			wantNameHeader:   "my-tool",
		},
		{
			name:             "prompts/get with future version",
			protocolVersion:  minVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "code_review"})},
			wantMethodHeader: "prompts/get",
			wantNameHeader:   "code_review",
		},
		{
			name:             "resources/read with future version",
			protocolVersion:  minVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///info.txt"})},
			wantMethodHeader: "resources/read",
			wantNameHeader:   "file:///info.txt",
		},
		{
			name:             "initialize sets method only",
			protocolVersion:  minVersionForStandardHeaders,
			msg:              &jsonrpc.Request{Method: "initialize", Params: mustMarshal(&InitializeParams{ProtocolVersion: minVersionForStandardHeaders})},
			wantMethodHeader: "initialize",
			wantNameHeader:   "",
		},
		{
			name:             "notification sets method only",
			protocolVersion:  minVersionForStandardHeaders,
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
			protocolVersion:  minVersionForStandardHeaders,
			msg:              nil,
			wantMethodHeader: "",
			wantNameHeader:   "",
		},
		{
			name:             "response message is ignored",
			protocolVersion:  minVersionForStandardHeaders,
			msg:              &jsonrpc.Response{},
			wantMethodHeader: "",
			wantNameHeader:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.protocolVersion != "" {
				header.Set(protocolVersionHeader, tt.protocolVersion)
			}

			setStandardHeaders(header, tt.msg)

			if got := header.Get(methodHeader); got != tt.wantMethodHeader {
				t.Errorf("MethodHeader = %q, want %q", got, tt.wantMethodHeader)
			}
			if got := header.Get(nameHeader); got != tt.wantNameHeader {
				t.Errorf("NameHeader = %q, want %q", got, tt.wantNameHeader)
			}
		})
	}
}

func TestValidateMcpHeaders(t *testing.T) {

	tests := []struct {
		name           string
		version        string
		methodHeader   string
		nameHeader     string
		msg            jsonrpc.Message
		wantErr        bool
		wantErrContain string
	}{
		{
			name:    "old version skips validation",
			version: protocolVersion20251125,
			msg:     &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr: false,
		},
		{
			name:    "empty version skips validation",
			version: "",
			msg:     &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr: false,
		},
		{
			name:           "missing Mcp-Method header",
			version:        minVersionForStandardHeaders,
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Method header",
		},
		{
			name:           "missing Mcp-Name for tools/call",
			version:        minVersionForStandardHeaders,
			methodHeader:   "tools/call",
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Name header",
		},
		{
			name:           "missing Mcp-Name for resources/read",
			version:        minVersionForStandardHeaders,
			methodHeader:   "resources/read",
			msg:            &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///info.txt"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Name header",
		},
		{
			name:           "missing Mcp-Name for prompts/get",
			version:        minVersionForStandardHeaders,
			methodHeader:   "prompts/get",
			msg:            &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "review"})},
			wantErr:        true,
			wantErrContain: "missing required Mcp-Name header",
		},
		{
			name:           "method mismatch",
			version:        minVersionForStandardHeaders,
			methodHeader:   "tools/call",
			msg:            &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "review"})},
			wantErr:        true,
			wantErrContain: "Mcp-Method header value 'tools/call' does not match body value 'prompts/get'",
		},
		{
			name:           "tool name mismatch",
			version:        minVersionForStandardHeaders,
			methodHeader:   "tools/call",
			nameHeader:     "wrong-tool",
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "right-tool"})},
			wantErr:        true,
			wantErrContain: "Mcp-Name header value 'wrong-tool' does not match body value 'right-tool'",
		},
		{
			name:           "resource URI mismatch",
			version:        minVersionForStandardHeaders,
			methodHeader:   "resources/read",
			nameHeader:     "file:///wrong.txt",
			msg:            &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///right.txt"})},
			wantErr:        true,
			wantErrContain: "Mcp-Name header value 'file:///wrong.txt' does not match body value 'file:///right.txt'",
		},
		{
			name:           "prompt name mismatch",
			version:        minVersionForStandardHeaders,
			methodHeader:   "prompts/get",
			nameHeader:     "wrong-prompt",
			msg:            &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "right-prompt"})},
			wantErr:        true,
			wantErrContain: "Mcp-Name header value 'wrong-prompt' does not match body value 'right-prompt'",
		},
		{
			name:           "method value is case-sensitive",
			version:        minVersionForStandardHeaders,
			methodHeader:   "TOOLS/CALL",
			nameHeader:     "my-tool",
			msg:            &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:        true,
			wantErrContain: "Mcp-Method header value 'TOOLS/CALL' does not match body value 'tools/call'",
		},
		{
			name:         "valid tools/call",
			version:      minVersionForStandardHeaders,
			methodHeader: "tools/call",
			nameHeader:   "my-tool",
			msg:          &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool"})},
			wantErr:      false,
		},
		{
			name:         "valid resources/read",
			version:      minVersionForStandardHeaders,
			methodHeader: "resources/read",
			nameHeader:   "file:///info.txt",
			msg:          &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///info.txt"})},
			wantErr:      false,
		},
		{
			name:         "valid prompts/get",
			version:      minVersionForStandardHeaders,
			methodHeader: "prompts/get",
			nameHeader:   "code_review",
			msg:          &jsonrpc.Request{Method: "prompts/get", Params: mustMarshal(&GetPromptParams{Name: "code_review"})},
			wantErr:      false,
		},
		{
			name:         "valid initialize (no name needed)",
			version:      minVersionForStandardHeaders,
			methodHeader: "initialize",
			msg:          &jsonrpc.Request{Method: "initialize", Params: mustMarshal(&InitializeParams{ProtocolVersion: minVersionForStandardHeaders})},
			wantErr:      false,
		},
		{
			name:         "valid notification (no name needed)",
			version:      minVersionForStandardHeaders,
			methodHeader: "notifications/initialized",
			msg:          &jsonrpc.Request{Method: "notifications/initialized"},
			wantErr:      false,
		},
		{
			name:         "tool name with hyphen",
			version:      minVersionForStandardHeaders,
			methodHeader: "tools/call",
			nameHeader:   "my-tool-name",
			msg:          &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my-tool-name"})},
			wantErr:      false,
		},
		{
			name:         "tool name with underscore",
			version:      minVersionForStandardHeaders,
			methodHeader: "tools/call",
			nameHeader:   "my_tool_name",
			msg:          &jsonrpc.Request{Method: "tools/call", Params: mustMarshal(&CallToolParams{Name: "my_tool_name"})},
			wantErr:      false,
		},
		{
			name:         "resource URI with special chars",
			version:      minVersionForStandardHeaders,
			methodHeader: "resources/read",
			nameHeader:   "file:///path/to/file%20name.txt",
			msg:          &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "file:///path/to/file%20name.txt"})},
			wantErr:      false,
		},
		{
			name:         "resource URI with query string",
			version:      minVersionForStandardHeaders,
			methodHeader: "resources/read",
			nameHeader:   "https://example.com/resource?id=123",
			msg:          &jsonrpc.Request{Method: "resources/read", Params: mustMarshal(&ReadResourceParams{URI: "https://example.com/resource?id=123"})},
			wantErr:      false,
		},
		{
			name:    "response message is ignored",
			version: minVersionForStandardHeaders,
			msg:     &jsonrpc.Response{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.version != "" {
				header.Set(protocolVersionHeader, tt.version)
			}
			if tt.methodHeader != "" {
				header.Set(methodHeader, tt.methodHeader)
			}
			if tt.nameHeader != "" {
				header.Set(nameHeader, tt.nameHeader)
			}

			err := validateMcpHeaders(header, tt.msg)
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
