// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestToolUseContent_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		content *ToolUseContent
		want    map[string]any
	}{
		{
			name: "basic tool use",
			content: &ToolUseContent{
				ID:   "tool_123",
				Name: "calculator",
				Input: map[string]any{
					"operation": "add",
					"x":         1.0,
					"y":         2.0,
				},
			},
			want: map[string]any{
				"type": "tool_use",
				"id":   "tool_123",
				"name": "calculator",
				"input": map[string]any{
					"operation": "add",
					"x":         1.0,
					"y":         2.0,
				},
			},
		},
		{
			name: "tool use with nil input",
			content: &ToolUseContent{
				ID:    "tool_456",
				Name:  "no_args_tool",
				Input: nil,
			},
			want: map[string]any{
				"type":  "tool_use",
				"id":    "tool_456",
				"name":  "no_args_tool",
				"input": map[string]any{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.content.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON() error = %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToolResultContent_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		content *ToolResultContent
		want    map[string]any
	}{
		{
			name: "basic tool result",
			content: &ToolResultContent{
				ToolUseID: "tool_123",
				Content:   []Content{&TextContent{Text: "42"}},
			},
			want: map[string]any{
				"type":      "tool_result",
				"toolUseId": "tool_123",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "42",
					},
				},
			},
		},
		{
			name: "tool result with error",
			content: &ToolResultContent{
				ToolUseID: "tool_456",
				Content:   []Content{&TextContent{Text: "division by zero"}},
				IsError:   true,
			},
			want: map[string]any{
				"type":      "tool_result",
				"toolUseId": "tool_456",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "division by zero",
					},
				},
				"isError": true,
			},
		},
		{
			name: "tool result with structured content",
			content: &ToolResultContent{
				ToolUseID:         "tool_789",
				Content:           []Content{&TextContent{Text: `{"result": 42}`}},
				StructuredContent: map[string]any{"result": 42.0},
			},
			want: map[string]any{
				"type":              "tool_result",
				"toolUseId":         "tool_789",
				"structuredContent": map[string]any{"result": 42.0},
				"content": []any{
					map[string]any{
						"type": "text",
						"text": `{"result": 42}`,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.content.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON() error = %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MarshalJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToolUseContent_UnmarshalJSON(t *testing.T) {
	jsonData := `{
		"type": "tool_use",
		"id": "tool_123",
		"name": "calculator",
		"input": {"x": 1, "y": 2}
	}`

	wire := &wireContent{}
	if err := json.Unmarshal([]byte(jsonData), wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	content, err := contentFromWire(wire, map[string]bool{"tool_use": true})
	if err != nil {
		t.Fatalf("contentFromWire() error = %v", err)
	}

	toolUse, ok := content.(*ToolUseContent)
	if !ok {
		t.Fatalf("expected *ToolUseContent, got %T", content)
	}

	if toolUse.ID != "tool_123" {
		t.Errorf("ID = %v, want %v", toolUse.ID, "tool_123")
	}
	if toolUse.Name != "calculator" {
		t.Errorf("Name = %v, want %v", toolUse.Name, "calculator")
	}
	if toolUse.Input["x"] != 1.0 || toolUse.Input["y"] != 2.0 {
		t.Errorf("Input = %v, want map with x=1, y=2", toolUse.Input)
	}
}

func TestToolResultContent_UnmarshalJSON(t *testing.T) {
	jsonData := `{
		"type": "tool_result",
		"toolUseId": "tool_123",
		"content": [{"type": "text", "text": "42"}],
		"isError": false
	}`

	wire := &wireContent{}
	if err := json.Unmarshal([]byte(jsonData), wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	content, err := contentFromWire(wire, map[string]bool{"tool_result": true})
	if err != nil {
		t.Fatalf("contentFromWire() error = %v", err)
	}

	toolResult, ok := content.(*ToolResultContent)
	if !ok {
		t.Fatalf("expected *ToolResultContent, got %T", content)
	}

	if toolResult.ToolUseID != "tool_123" {
		t.Errorf("ToolUseID = %v, want %v", toolResult.ToolUseID, "tool_123")
	}
	if toolResult.IsError {
		t.Errorf("IsError = %v, want false", toolResult.IsError)
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("len(Content) = %v, want 1", len(toolResult.Content))
	}
	textContent, ok := toolResult.Content[0].(*TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", toolResult.Content[0])
	}
	if textContent.Text != "42" {
		t.Errorf("Text = %v, want %v", textContent.Text, "42")
	}
}

func TestCreateMessageWithToolsResult_ToolUseContent(t *testing.T) {
	// Test that CreateMessageWithToolsResult can unmarshal tool_use content
	jsonData := `{
		"content": {"type": "tool_use", "id": "tool_1", "name": "calculator", "input": {"x": 1}},
		"model": "test-model",
		"role": "assistant",
		"stopReason": "toolUse"
	}`

	var result CreateMessageWithToolsResult
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if result.Model != "test-model" {
		t.Errorf("Model = %v, want %v", result.Model, "test-model")
	}
	if result.StopReason != "toolUse" {
		t.Errorf("StopReason = %v, want %v", result.StopReason, "toolUse")
	}

	if len(result.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(result.Content))
	}
	toolUse, ok := result.Content[0].(*ToolUseContent)
	if !ok {
		t.Fatalf("Content[0] expected *ToolUseContent, got %T", result.Content[0])
	}
	if toolUse.ID != "tool_1" {
		t.Errorf("Content.ID = %v, want %v", toolUse.ID, "tool_1")
	}
	if toolUse.Name != "calculator" {
		t.Errorf("Content.Name = %v, want %v", toolUse.Name, "calculator")
	}
}

func TestSamplingMessage_ToolUseContent(t *testing.T) {
	// Test that SamplingMessage can unmarshal tool_use content (assistant role)
	jsonData := `{
		"content": {"type": "tool_use", "id": "tool_1", "name": "calc", "input": {}},
		"role": "assistant"
	}`

	var msg SamplingMessage
	if err := json.Unmarshal([]byte(jsonData), &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if msg.Role != "assistant" {
		t.Errorf("Role = %v, want %v", msg.Role, "assistant")
	}

	toolUse, ok := msg.Content.(*ToolUseContent)
	if !ok {
		t.Fatalf("Content expected *ToolUseContent, got %T", msg.Content)
	}
	if toolUse.ID != "tool_1" {
		t.Errorf("Content.ID = %v, want %v", toolUse.ID, "tool_1")
	}
}

func TestSamplingMessage_ToolResultContent(t *testing.T) {
	// Test that SamplingMessage can unmarshal tool_result content (user role)
	jsonData := `{
		"content": {"type": "tool_result", "toolUseId": "tool_1", "content": [{"type": "text", "text": "42"}]},
		"role": "user"
	}`

	var msg SamplingMessage
	if err := json.Unmarshal([]byte(jsonData), &msg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if msg.Role != "user" {
		t.Errorf("Role = %v, want %v", msg.Role, "user")
	}

	toolResult, ok := msg.Content.(*ToolResultContent)
	if !ok {
		t.Fatalf("Content expected *ToolResultContent, got %T", msg.Content)
	}
	if toolResult.ToolUseID != "tool_1" {
		t.Errorf("Content.ToolUseID = %v, want %v", toolResult.ToolUseID, "tool_1")
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("len(Content.Content) = %v, want 1", len(toolResult.Content))
	}
}

func TestSamplingCapabilities_WithTools(t *testing.T) {
	caps := &SamplingCapabilities{
		Tools:   &SamplingToolsCapabilities{},
		Context: &SamplingContextCapabilities{},
	}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var caps2 SamplingCapabilities
	if err := json.Unmarshal(data, &caps2); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if caps2.Tools == nil {
		t.Error("Tools capability should not be nil")
	}
	if caps2.Context == nil {
		t.Error("Context capability should not be nil")
	}
}

func TestSamplingCapabilities_Empty(t *testing.T) {
	// Test backward compatibility - empty struct should marshal/unmarshal correctly
	caps := &SamplingCapabilities{}

	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var caps2 SamplingCapabilities
	if err := json.Unmarshal(data, &caps2); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if caps2.Tools != nil {
		t.Error("Tools capability should be nil for empty capabilities")
	}
	if caps2.Context != nil {
		t.Error("Context capability should be nil for empty capabilities")
	}
}

func TestCreateMessageWithToolsParams(t *testing.T) {
	params := &CreateMessageWithToolsParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessageV2{
			{
				Role:    "user",
				Content: []Content{&TextContent{Text: "Calculate 1+1"}},
			},
		},
		Tools: []*Tool{
			{
				Name:        "calculator",
				Description: "A calculator tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "number"},
						"y": map[string]any{"type": "number"},
					},
				},
			},
		},
		ToolChoice: &ToolChoice{Mode: "auto"},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var params2 CreateMessageWithToolsParams
	if err := json.Unmarshal(data, &params2); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(params2.Tools) != 1 {
		t.Fatalf("len(Tools) = %v, want 1", len(params2.Tools))
	}
	if params2.Tools[0].Name != "calculator" {
		t.Errorf("Tools[0].Name = %v, want %v", params2.Tools[0].Name, "calculator")
	}
	if params2.ToolChoice == nil || params2.ToolChoice.Mode != "auto" {
		t.Errorf("ToolChoice.Mode = %v, want %v", params2.ToolChoice, &ToolChoice{Mode: "auto"})
	}
}

func TestToolChoice_Modes(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{"auto", "auto"},
		{"required", "required"},
		{"none", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &ToolChoice{Mode: tt.mode}
			data, err := json.Marshal(tc)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			var tc2 ToolChoice
			if err := json.Unmarshal(data, &tc2); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if tc2.Mode != tt.mode {
				t.Errorf("Mode = %v, want %v", tc2.Mode, tt.mode)
			}
		})
	}
}

// Integration tests

func TestSamplingWithTools_Integration(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Track what the client received
	var receivedParams *CreateMessageWithToolsParams

	// Client with tools capability, using CreateMessageWithToolsHandler
	client := NewClient(testImpl, &ClientOptions{
		CreateMessageWithToolsHandler: func(_ context.Context, req *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
			receivedParams = req.Params
			// Return a tool use response
			return &CreateMessageWithToolsResult{
				Model: "test-model",
				Role:  "assistant",
				Content: []Content{&ToolUseContent{
					ID:    "tool_call_1",
					Name:  "calculator",
					Input: map[string]any{"x": 1.0, "y": 2.0},
				}},
				StopReason: "toolUse",
			}, nil
		},
		Capabilities: &ClientCapabilities{
			Sampling: &SamplingCapabilities{Tools: &SamplingToolsCapabilities{}},
		},
	})

	server := NewServer(testImpl, nil)
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Server sends CreateMessageWithTools
	result, err := ss.CreateMessageWithTools(ctx, &CreateMessageWithToolsParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessageV2{
			{Role: "user", Content: []Content{&TextContent{Text: "Calculate 1+2"}}},
		},
		Tools: []*Tool{
			{
				Name:        "calculator",
				Description: "A calculator",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "number"},
						"y": map[string]any{"type": "number"},
					},
				},
			},
		},
		ToolChoice: &ToolChoice{Mode: "auto"},
	})
	if err != nil {
		t.Fatalf("CreateMessageWithTools() error = %v", err)
	}

	// Verify client received the tools
	if receivedParams == nil {
		t.Fatal("client did not receive params")
	}
	if len(receivedParams.Tools) != 1 {
		t.Errorf("client received %d tools, want 1", len(receivedParams.Tools))
	}
	if receivedParams.Tools[0].Name != "calculator" {
		t.Errorf("tool name = %v, want calculator", receivedParams.Tools[0].Name)
	}
	if receivedParams.ToolChoice == nil || receivedParams.ToolChoice.Mode != "auto" {
		t.Errorf("tool choice mode = %v, want auto", receivedParams.ToolChoice)
	}

	// Verify server received the tool use response
	if result.StopReason != "toolUse" {
		t.Errorf("StopReason = %v, want toolUse", result.StopReason)
	}
	if len(result.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(result.Content))
	}
	toolUse, ok := result.Content[0].(*ToolUseContent)
	if !ok {
		t.Fatalf("Content[0] type = %T, want *ToolUseContent", result.Content[0])
	}
	if toolUse.ID != "tool_call_1" {
		t.Errorf("ToolUse.ID = %v, want tool_call_1", toolUse.ID)
	}
	if toolUse.Name != "calculator" {
		t.Errorf("ToolUse.Name = %v, want calculator", toolUse.Name)
	}
}

func TestSamplingWithToolResult_Integration(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Track messages received by client
	var receivedMessages []*SamplingMessage

	client := NewClient(testImpl, &ClientOptions{
		CreateMessageHandler: func(_ context.Context, req *CreateMessageRequest) (*CreateMessageResult, error) {
			receivedMessages = req.Params.Messages
			return &CreateMessageResult{
				Model:   "test-model",
				Role:    "assistant",
				Content: &TextContent{Text: "The result is 3"},
			}, nil
		},
		Capabilities: &ClientCapabilities{
			Sampling: &SamplingCapabilities{Tools: &SamplingToolsCapabilities{}},
		},
	})

	server := NewServer(testImpl, nil)
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Server sends CreateMessage with tool result in messages
	_, err = ss.CreateMessage(ctx, &CreateMessageParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessage{
			{Role: "user", Content: &TextContent{Text: "Calculate 1+2"}},
			{Role: "assistant", Content: &ToolUseContent{
				ID:    "tool_1",
				Name:  "calculator",
				Input: map[string]any{"x": 1.0, "y": 2.0},
			}},
			{Role: "user", Content: &ToolResultContent{
				ToolUseID: "tool_1",
				Content:   []Content{&TextContent{Text: "3"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}

	// Verify client received all messages including tool content
	if len(receivedMessages) != 3 {
		t.Fatalf("received %d messages, want 3", len(receivedMessages))
	}

	// Check first message is text
	if _, ok := receivedMessages[0].Content.(*TextContent); !ok {
		t.Errorf("message[0] content type = %T, want *TextContent", receivedMessages[0].Content)
	}

	// Check second message is tool use
	toolUse, ok := receivedMessages[1].Content.(*ToolUseContent)
	if !ok {
		t.Fatalf("message[1] content type = %T, want *ToolUseContent", receivedMessages[1].Content)
	}
	if toolUse.ID != "tool_1" {
		t.Errorf("toolUse.ID = %v, want tool_1", toolUse.ID)
	}

	// Check third message is tool result
	toolResult, ok := receivedMessages[2].Content.(*ToolResultContent)
	if !ok {
		t.Fatalf("message[2] content type = %T, want *ToolResultContent", receivedMessages[2].Content)
	}
	if toolResult.ToolUseID != "tool_1" {
		t.Errorf("toolResult.ToolUseID = %v, want tool_1", toolResult.ToolUseID)
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("toolResult.Content len = %d, want 1", len(toolResult.Content))
	}
	if tc, ok := toolResult.Content[0].(*TextContent); !ok || tc.Text != "3" {
		t.Errorf("toolResult.Content[0] = %v, want TextContent with '3'", toolResult.Content[0])
	}
}

func TestSamplingToolsCapability_Integration(t *testing.T) {
	ctx := context.Background()

	t.Run("client advertises tools capability", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		client := NewClient(testImpl, &ClientOptions{
			CreateMessageHandler: func(_ context.Context, _ *CreateMessageRequest) (*CreateMessageResult, error) {
				return &CreateMessageResult{Model: "m", Content: &TextContent{}}, nil
			},
			Capabilities: &ClientCapabilities{
				Sampling: &SamplingCapabilities{
					Tools:   &SamplingToolsCapabilities{},
					Context: &SamplingContextCapabilities{},
				},
			},
		})

		server := NewServer(testImpl, nil)
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// Check server sees client capabilities
		caps := ss.InitializeParams().Capabilities
		if caps.Sampling == nil {
			t.Fatal("client should advertise sampling capability")
		}
		if caps.Sampling.Tools == nil {
			t.Error("client should advertise sampling.tools capability")
		}
		if caps.Sampling.Context == nil {
			t.Error("client should advertise sampling.context capability")
		}
	})

	t.Run("client without tools capability", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		client := NewClient(testImpl, &ClientOptions{
			CreateMessageHandler: func(_ context.Context, _ *CreateMessageRequest) (*CreateMessageResult, error) {
				return &CreateMessageResult{Model: "m", Content: &TextContent{}}, nil
			},
			// No Capabilities.Sampling.Tools set
		})

		server := NewServer(testImpl, nil)
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// Check server sees client capabilities
		caps := ss.InitializeParams().Capabilities
		if caps.Sampling == nil {
			t.Fatal("client should advertise sampling capability")
		}
		if caps.Sampling.Tools != nil {
			t.Error("client should NOT advertise sampling.tools capability")
		}
		if caps.Sampling.Context != nil {
			t.Error("client should NOT advertise sampling.context capability")
		}
	})

	t.Run("CreateMessageWithToolsHandler infers tools capability", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		client := NewClient(testImpl, &ClientOptions{
			CreateMessageWithToolsHandler: func(_ context.Context, _ *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
				return &CreateMessageWithToolsResult{Model: "m", Content: []Content{&TextContent{}}}, nil
			},
			// No explicit Capabilities set — tools should be inferred.
		})

		server := NewServer(testImpl, nil)
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := client.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		caps := ss.InitializeParams().Capabilities
		if caps.Sampling == nil {
			t.Fatal("client should advertise sampling capability")
		}
		if caps.Sampling.Tools == nil {
			t.Error("client should infer sampling.tools capability from CreateMessageWithToolsHandler")
		}
	})
}

func TestSamplingToolResultWithError_Integration(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	var receivedMessages []*SamplingMessage

	client := NewClient(testImpl, &ClientOptions{
		CreateMessageHandler: func(_ context.Context, req *CreateMessageRequest) (*CreateMessageResult, error) {
			receivedMessages = req.Params.Messages
			return &CreateMessageResult{
				Model:   "test-model",
				Role:    "assistant",
				Content: &TextContent{Text: "I see the tool failed"},
			}, nil
		},
		Capabilities: &ClientCapabilities{
			Sampling: &SamplingCapabilities{Tools: &SamplingToolsCapabilities{}},
		},
	})

	server := NewServer(testImpl, nil)
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Server sends CreateMessage with error tool result
	_, err = ss.CreateMessage(ctx, &CreateMessageParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessage{
			{Role: "user", Content: &ToolResultContent{
				ToolUseID: "tool_1",
				Content:   []Content{&TextContent{Text: "division by zero"}},
				IsError:   true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}

	if len(receivedMessages) != 1 {
		t.Fatalf("received %d messages, want 1", len(receivedMessages))
	}

	toolResult, ok := receivedMessages[0].Content.(*ToolResultContent)
	if !ok {
		t.Fatalf("content type = %T, want *ToolResultContent", receivedMessages[0].Content)
	}
	if !toolResult.IsError {
		t.Error("IsError should be true")
	}
	if toolResult.ToolUseID != "tool_1" {
		t.Errorf("ToolUseID = %v, want tool_1", toolResult.ToolUseID)
	}
}

func TestToolResultContent_ImageNestedContent(t *testing.T) {
	// Verify non-text nested content in ToolResultContent works.
	jsonData := `{
		"type": "tool_result",
		"toolUseId": "t1",
		"content": [
			{"type": "image", "mimeType": "image/png", "data": "YWJj"}
		]
	}`

	wire := &wireContent{}
	if err := json.Unmarshal([]byte(jsonData), wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	content, err := contentFromWire(wire, map[string]bool{"tool_result": true})
	if err != nil {
		t.Fatalf("contentFromWire() error = %v", err)
	}

	toolResult, ok := content.(*ToolResultContent)
	if !ok {
		t.Fatalf("expected *ToolResultContent, got %T", content)
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(toolResult.Content))
	}
	img, ok := toolResult.Content[0].(*ImageContent)
	if !ok {
		t.Fatalf("nested content type = %T, want *ImageContent", toolResult.Content[0])
	}
	if img.MIMEType != "image/png" {
		t.Errorf("MIMEType = %v, want image/png", img.MIMEType)
	}
}

func TestToolUseContent_MetaRoundTrip(t *testing.T) {
	// Verify Meta round-trips through marshal/unmarshal.
	orig := &ToolUseContent{
		ID:    "t1",
		Name:  "calc",
		Input: map[string]any{"x": 1.0},
		Meta:  Meta{"requestId": "req-123"},
	}

	data, err := orig.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	wire := &wireContent{}
	if err := json.Unmarshal(data, wire); err != nil {
		t.Fatalf("Unmarshal wire error = %v", err)
	}

	content, err := contentFromWire(wire, map[string]bool{"tool_use": true})
	if err != nil {
		t.Fatalf("contentFromWire() error = %v", err)
	}

	got, ok := content.(*ToolUseContent)
	if !ok {
		t.Fatalf("type = %T, want *ToolUseContent", content)
	}
	if got.Meta["requestId"] != "req-123" {
		t.Errorf("Meta[requestId] = %v, want req-123", got.Meta["requestId"])
	}
}

func TestParallelToolCalls_Integration(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Client returns parallel tool use results
	client := NewClient(testImpl, &ClientOptions{
		CreateMessageWithToolsHandler: func(_ context.Context, req *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
			return &CreateMessageWithToolsResult{
				Model: "test-model",
				Role:  "assistant",
				Content: []Content{
					&ToolUseContent{ID: "call_1", Name: "weather", Input: map[string]any{"city": "SF"}},
					&ToolUseContent{ID: "call_2", Name: "weather", Input: map[string]any{"city": "NY"}},
				},
				StopReason: "toolUse",
			}, nil
		},
		Capabilities: &ClientCapabilities{
			Sampling: &SamplingCapabilities{Tools: &SamplingToolsCapabilities{}},
		},
	})

	server := NewServer(testImpl, nil)
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	result, err := ss.CreateMessageWithTools(ctx, &CreateMessageWithToolsParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessageV2{
			{Role: "user", Content: []Content{&TextContent{Text: "Weather in SF and NY"}}},
		},
		Tools: []*Tool{
			{Name: "weather", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateMessageWithTools() error = %v", err)
	}

	if len(result.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(result.Content))
	}
	for i, c := range result.Content {
		tu, ok := c.(*ToolUseContent)
		if !ok {
			t.Fatalf("Content[%d] type = %T, want *ToolUseContent", i, c)
		}
		if tu.Name != "weather" {
			t.Errorf("Content[%d].Name = %v, want weather", i, tu.Name)
		}
	}
	if result.Content[0].(*ToolUseContent).ID != "call_1" {
		t.Errorf("Content[0].ID = %v, want call_1", result.Content[0].(*ToolUseContent).ID)
	}
	if result.Content[1].(*ToolUseContent).ID != "call_2" {
		t.Errorf("Content[1].ID = %v, want call_2", result.Content[1].(*ToolUseContent).ID)
	}
}

func TestCreateMessageWithToolsResult_ArrayRoundTrip(t *testing.T) {
	// Marshal multi-content, unmarshal, verify.
	orig := &CreateMessageWithToolsResult{
		Model: "test",
		Role:  "assistant",
		Content: []Content{
			&ToolUseContent{ID: "t1", Name: "calc", Input: map[string]any{"x": 1.0}},
			&ToolUseContent{ID: "t2", Name: "search", Input: map[string]any{"q": "hi"}},
		},
		StopReason: "toolUse",
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var got CreateMessageWithToolsResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(got.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(got.Content))
	}
	for i, c := range got.Content {
		tu, ok := c.(*ToolUseContent)
		if !ok {
			t.Fatalf("Content[%d] type = %T, want *ToolUseContent", i, c)
		}
		origTU := orig.Content[i].(*ToolUseContent)
		if tu.ID != origTU.ID || tu.Name != origTU.Name {
			t.Errorf("Content[%d] = %+v, want %+v", i, tu, origTU)
		}
	}
}

func TestCreateMessageWithToolsResult_SingleContentBackwardCompat(t *testing.T) {
	// Single-element Content marshals as object (not array).
	result := &CreateMessageWithToolsResult{
		Model:   "test",
		Role:    "assistant",
		Content: []Content{&TextContent{Text: "hello"}},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw error = %v", err)
	}

	content := raw["content"]
	for _, b := range content {
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		if b == '[' {
			t.Errorf("single-element Content marshaled as array, want object")
		}
		break
	}
}

func TestNewClient_BothHandlersPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when both handlers set")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "CreateMessageHandler") {
			t.Errorf("unexpected panic: %v", r)
		}
	}()
	NewClient(testImpl, &ClientOptions{
		CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
			return nil, nil
		},
		CreateMessageWithToolsHandler: func(context.Context, *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
			return nil, nil
		},
	})
}

func TestCreateMessage_MultipleContentError(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Client returns multiple content blocks via CreateMessageWithToolsHandler
	client := NewClient(testImpl, &ClientOptions{
		CreateMessageWithToolsHandler: func(_ context.Context, _ *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
			return &CreateMessageWithToolsResult{
				Model: "test",
				Role:  "assistant",
				Content: []Content{
					&TextContent{Text: "a"},
					&TextContent{Text: "b"},
				},
			}, nil
		},
	})

	server := NewServer(testImpl, nil)
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Server calls CreateMessage (singular), should get error
	_, err = ss.CreateMessage(ctx, &CreateMessageParams{
		MaxTokens: 100,
		Messages:  []*SamplingMessage{{Role: "user", Content: &TextContent{Text: "hi"}}},
	})
	if err == nil {
		t.Fatal("expected error for multiple content blocks")
	}
	if !strings.Contains(err.Error(), "CreateMessageWithTools") {
		t.Errorf("error should mention CreateMessageWithTools, got: %v", err)
	}
}

func TestUnmarshalContent_NullJSON(t *testing.T) {
	// JSON null should be rejected.
	jsonData := `{"content": null, "model": "m", "role": "assistant"}`
	var result CreateMessageWithToolsResult
	if err := json.Unmarshal([]byte(jsonData), &result); err == nil {
		t.Error("expected error for null content")
	}
}

func TestUnmarshalContent_EmptyArray(t *testing.T) {
	// Empty array should produce empty (non-nil) slice.
	jsonData := `{"content": [], "model": "m", "role": "assistant"}`
	var result CreateMessageWithToolsResult
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Content == nil {
		t.Error("Content should be non-nil empty slice, got nil")
	}
	if len(result.Content) != 0 {
		t.Errorf("len(Content) = %d, want 0", len(result.Content))
	}
}

func TestSamplingMessageV2_EmptyContent(t *testing.T) {
	msg := &SamplingMessageV2{
		Role:    "user",
		Content: []Content{},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got SamplingMessageV2
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(got.Content) != 0 {
		t.Errorf("len(Content) = %d, want 0", len(got.Content))
	}
}

func TestSamplingMessageV2_MixedContent(t *testing.T) {
	// Text + tool_use in the same message (valid per spec for assistant).
	msg := &SamplingMessageV2{
		Role: "assistant",
		Content: []Content{
			&TextContent{Text: "Let me check the weather."},
			&ToolUseContent{ID: "c1", Name: "weather", Input: map[string]any{"city": "SF"}},
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got SamplingMessageV2
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(got.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(got.Content))
	}
	if _, ok := got.Content[0].(*TextContent); !ok {
		t.Errorf("Content[0] type = %T, want *TextContent", got.Content[0])
	}
	if _, ok := got.Content[1].(*ToolUseContent); !ok {
		t.Errorf("Content[1] type = %T, want *ToolUseContent", got.Content[1])
	}
}

func TestCreateMessageWithToolsResult_RejectsToolResult(t *testing.T) {
	// tool_result should not be valid in a result (assistant role).
	jsonData := `{
		"content": {"type": "tool_result", "toolUseId": "t1", "content": []},
		"model": "m",
		"role": "assistant"
	}`
	var result CreateMessageWithToolsResult
	if err := json.Unmarshal([]byte(jsonData), &result); err == nil {
		t.Error("expected error for tool_result in CreateMessageWithToolsResult")
	}
}

func TestToBase_Conversion(t *testing.T) {
	params := &CreateMessageWithToolsParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessageV2{
			{Role: "user", Content: []Content{&TextContent{Text: "hello"}}},
			{Role: "assistant", Content: []Content{
				&ToolUseContent{ID: "c1", Name: "calc", Input: map[string]any{}},
				&ToolUseContent{ID: "c2", Name: "search", Input: map[string]any{}},
			}},
		},
		Tools:      []*Tool{{Name: "calc"}},
		ToolChoice: &ToolChoice{Mode: "auto"},
	}
	base := params.toBase()

	// Tools and ToolChoice should be gone
	if base.MaxTokens != 1000 {
		t.Errorf("MaxTokens = %d, want 1000", base.MaxTokens)
	}
	if len(base.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(base.Messages))
	}
	// First message: single content preserved
	if tc, ok := base.Messages[0].Content.(*TextContent); !ok || tc.Text != "hello" {
		t.Errorf("Messages[0].Content = %v, want TextContent{hello}", base.Messages[0].Content)
	}
	// Second message: only first content block kept
	if tu, ok := base.Messages[1].Content.(*ToolUseContent); !ok || tu.ID != "c1" {
		t.Errorf("Messages[1].Content = %v, want ToolUseContent{c1}", base.Messages[1].Content)
	}
}

func TestToWithTools_Conversion(t *testing.T) {
	result := &CreateMessageResult{
		Model:      "test",
		Role:       "assistant",
		Content:    &TextContent{Text: "hello"},
		StopReason: "endTurn",
	}
	wt := result.toWithTools()
	if wt.Model != "test" {
		t.Errorf("Model = %v, want test", wt.Model)
	}
	if len(wt.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(wt.Content))
	}
	if tc, ok := wt.Content[0].(*TextContent); !ok || tc.Text != "hello" {
		t.Errorf("Content[0] = %v, want TextContent{hello}", wt.Content[0])
	}
}

func TestToWithTools_NilContent(t *testing.T) {
	result := &CreateMessageResult{
		Model: "test",
		Role:  "assistant",
	}
	wt := result.toWithTools()
	if wt.Content != nil {
		t.Errorf("Content = %v, want nil", wt.Content)
	}
}

func TestClientCapabilities_CloneSampling(t *testing.T) {
	caps := &ClientCapabilities{
		Sampling: &SamplingCapabilities{
			Tools:   &SamplingToolsCapabilities{},
			Context: &SamplingContextCapabilities{},
		},
	}
	cloned := caps.clone()

	// Verify deep copy — Sampling pointer should differ.
	// (Tools and Context are empty structs, so Go may reuse the same address;
	// we just check they're non-nil and that mutating Sampling doesn't alias.)
	if cloned.Sampling == caps.Sampling {
		t.Error("Sampling pointer should differ after clone")
	}
	if cloned.Sampling.Tools == nil {
		t.Error("cloned Sampling.Tools should not be nil")
	}
	if cloned.Sampling.Context == nil {
		t.Error("cloned Sampling.Context should not be nil")
	}
	// Verify mutation doesn't affect original.
	cloned.Sampling.Tools = nil
	if caps.Sampling.Tools == nil {
		t.Error("modifying cloned Sampling.Tools should not affect original")
	}
}
