// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestToolHandler_PanicRecovery verifies that a panicking tool handler does
// not crash the server process. The panic should be recovered and returned
// as a JSON-RPC internal error to the client.
func TestToolHandler_PanicRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	AddTool(s, &Tool{
		Name:        "panic-tool",
		Description: "a tool that panics",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(_ context.Context, _ *CallToolRequest, _ map[string]any) (*CallToolResult, any, error) {
		panic("deliberate panic in tool handler")
	})

	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Call the panicking tool. Without recovery, this crashes the process.
	// With recovery, we get an error response.
	_, err = cs.CallTool(ctx, &CallToolParams{Name: "panic-tool"})

	// We expect an error (the panic is caught and returned as internal error).
	if err == nil {
		t.Fatal("expected error from panicking tool handler, got success")
	}
	// The important thing is we reached this line: the process didn't crash.
}

// TestToolHandler_PanicDoesNotAffectSubsequentCalls verifies that after a
// panic in one tool handler, the server continues to serve other requests.
func TestToolHandler_PanicDoesNotAffectSubsequentCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	AddTool(s, &Tool{
		Name:        "panic-tool",
		Description: "a tool that panics",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(_ context.Context, _ *CallToolRequest, _ map[string]any) (*CallToolResult, any, error) {
		panic("deliberate panic")
	})
	AddTool(s, &Tool{
		Name:        "safe-tool",
		Description: "a tool that works",
		InputSchema: &jsonschema.Schema{Type: "object"},
	}, func(_ context.Context, _ *CallToolRequest, _ map[string]any) (*CallToolResult, any, error) {
		return &CallToolResult{Content: []Content{&TextContent{Text: "ok"}}}, nil, nil
	})

	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// First: call the panicking tool.
	_, _ = cs.CallTool(ctx, &CallToolParams{Name: "panic-tool"})

	// Second: call the safe tool. This should succeed, proving the server
	// is still alive after the panic.
	result, err := cs.CallTool(ctx, &CallToolParams{Name: "safe-tool"})
	if err != nil {
		t.Fatalf("safe tool call failed after panic: %v", err)
	}
	if result == nil {
		t.Fatal("expected result from safe tool, got nil")
	}
}
