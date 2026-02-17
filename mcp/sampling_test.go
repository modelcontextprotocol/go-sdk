// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

// TODO: move other sampling-related tests to this file.

import (
	"context"
	"strings"
	"testing"
)

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

func TestSamplingToolsCapabilities(t *testing.T) {
	ctx := context.Background()

	t.Run("client with explicit tools capability", func(t *testing.T) {
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

	t.Run("client with inferred tools capability", func(t *testing.T) {
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

	// Server sends CreateMessage with error tool result, preceded by
	// the original request and tool use for a more realistic scenario.
	_, err = ss.CreateMessage(ctx, &CreateMessageParams{
		MaxTokens: 1000,
		Messages: []*SamplingMessage{
			{Role: "user", Content: &TextContent{Text: "Divide 1 by 0"}},
			{Role: "assistant", Content: &ToolUseContent{
				ID:    "tool_1",
				Name:  "calculator",
				Input: map[string]any{"op": "div", "x": 1.0, "y": 0.0},
			}},
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

	if len(receivedMessages) != 3 {
		t.Fatalf("received %d messages, want 3", len(receivedMessages))
	}

	toolResult, ok := receivedMessages[2].Content.(*ToolResultContent)
	if !ok {
		t.Fatalf("content type = %T, want *ToolResultContent", receivedMessages[2].Content)
	}
	if !toolResult.IsError {
		t.Error("IsError should be true")
	}
	if toolResult.ToolUseID != "tool_1" {
		t.Errorf("ToolUseID = %v, want tool_1", toolResult.ToolUseID)
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
