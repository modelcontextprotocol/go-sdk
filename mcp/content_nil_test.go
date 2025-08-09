// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file contains tests to verify that UnmarshalJSON methods for Content types
// don't panic when unmarshaling onto nil pointers, as requested in GitHub issue #205.
//
// NOTE: The contentFromWire function has been fixed to handle nil wire.Content
// gracefully by returning an error instead of panicking.

package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestContentUnmarshalNil(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		content interface{}
	}{
		{
			name:    "CallToolResult nil Content",
			json:    `{"content":[{"type":"text","text":"hello"}]}`,
			content: &mcp.CallToolResult{},
		},
		{
			name:    "CreateMessageResult nil Content",
			json:    `{"content":{"type":"text","text":"hello"},"model":"test","role":"user"}`,
			content: &mcp.CreateMessageResult{},
		},
		{
			name:    "PromptMessage nil Content",
			json:    `{"content":{"type":"text","text":"hello"},"role":"user"}`,
			content: &mcp.PromptMessage{},
		},
		{
			name:    "SamplingMessage nil Content",
			json:    `{"content":{"type":"text","text":"hello"},"role":"user"}`,
			content: &mcp.SamplingMessage{},
		},
		{
			name:    "CallToolResultFor nil Content",
			json:    `{"content":[{"type":"text","text":"hello"}]}`,
			content: &mcp.CallToolResultFor[string]{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that unmarshaling doesn't panic on nil Content fields
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("UnmarshalJSON panicked: %v", r)
				}
			}()

			err := json.Unmarshal([]byte(tt.json), tt.content)
			if err != nil {
				t.Errorf("UnmarshalJSON failed: %v", err)
			}

			// Verify that the Content field was properly populated
			switch v := tt.content.(type) {
			case *mcp.CallToolResult:
				if len(v.Content) == 0 {
					t.Error("CallToolResult.Content was not populated")
				}
				if _, ok := v.Content[0].(*mcp.TextContent); !ok {
					t.Error("CallToolResult.Content[0] is not TextContent")
				}
			case *mcp.CallToolResultFor[string]:
				if len(v.Content) == 0 {
					t.Error("CallToolResultFor.Content was not populated")
				}
				if _, ok := v.Content[0].(*mcp.TextContent); !ok {
					t.Error("CallToolResultFor.Content[0] is not TextContent")
				}
			case *mcp.CreateMessageResult:
				if v.Content == nil {
					t.Error("CreateMessageResult.Content was not populated")
				}
				if _, ok := v.Content.(*mcp.TextContent); !ok {
					t.Error("CreateMessageResult.Content is not TextContent")
				}
			case *mcp.PromptMessage:
				if v.Content == nil {
					t.Error("PromptMessage.Content was not populated")
				}
				if _, ok := v.Content.(*mcp.TextContent); !ok {
					t.Error("PromptMessage.Content is not TextContent")
				}
			case *mcp.SamplingMessage:
				if v.Content == nil {
					t.Error("SamplingMessage.Content was not populated")
				}
				if _, ok := v.Content.(*mcp.TextContent); !ok {
					t.Error("SamplingMessage.Content is not TextContent")
				}
			}
		})
	}
}

func TestContentUnmarshalNilWithDifferentTypes(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		content     interface{}
		expectError bool
	}{
		{
			name:        "ImageContent",
			json:        `{"content":{"type":"image","mimeType":"image/png","data":"YTFiMmMz"}}`,
			content:     &mcp.CreateMessageResult{},
			expectError: false,
		},
		{
			name:        "AudioContent",
			json:        `{"content":{"type":"audio","mimeType":"audio/wav","data":"YTFiMmMz"}}`,
			content:     &mcp.CreateMessageResult{},
			expectError: false,
		},
		{
			name:        "ResourceLink",
			json:        `{"content":{"type":"resource_link","uri":"file:///test","name":"test"}}`,
			content:     &mcp.CreateMessageResult{},
			expectError: true, // CreateMessageResult only allows text, image, audio
		},
		{
			name:        "EmbeddedResource",
			json:        `{"content":{"type":"resource","resource":{"uri":"file://test","text":"test"}}}`,
			content:     &mcp.CreateMessageResult{},
			expectError: true, // CreateMessageResult only allows text, image, audio
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that unmarshaling doesn't panic on nil Content fields
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("UnmarshalJSON panicked: %v", r)
				}
			}()

			err := json.Unmarshal([]byte(tt.json), tt.content)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Verify that the Content field was properly populated for successful cases
			if !tt.expectError {
				if result, ok := tt.content.(*mcp.CreateMessageResult); ok {
					if result.Content == nil {
						t.Error("CreateMessageResult.Content was not populated")
					}
				}
			}
		})
	}
}

func TestContentUnmarshalNilWithEmptyContent(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		content     interface{}
		expectError bool
	}{
		{
			name:        "Empty Content array",
			json:        `{"content":[]}`,
			content:     &mcp.CallToolResult{},
			expectError: false,
		},
		{
			name:        "Missing Content field",
			json:        `{"model":"test","role":"user"}`,
			content:     &mcp.CreateMessageResult{},
			expectError: true, // Content field is required for CreateMessageResult
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that unmarshaling doesn't panic on nil Content fields
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("UnmarshalJSON panicked: %v", r)
				}
			}()

			err := json.Unmarshal([]byte(tt.json), tt.content)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestContentUnmarshalNilWithInvalidContent(t *testing.T) {
	tests := []struct {
		name        string
		json        string
		content     interface{}
		expectError bool
	}{
		{
			name:        "Invalid content type",
			json:        `{"content":{"type":"invalid","text":"hello"}}`,
			content:     &mcp.CreateMessageResult{},
			expectError: true,
		},
		{
			name:        "Missing type field",
			json:        `{"content":{"text":"hello"}}`,
			content:     &mcp.CreateMessageResult{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test that unmarshaling doesn't panic on nil Content fields
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("UnmarshalJSON panicked: %v", r)
				}
			}()

			err := json.Unmarshal([]byte(tt.json), tt.content)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}
