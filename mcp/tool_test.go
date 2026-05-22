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
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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

func TestToolErrorHandling(t *testing.T) {
	// Construct server and add both tools at the top level
	server := NewServer(testImpl, nil)

	// Create a tool that returns a structured error
	structuredErrorHandler := func(ctx context.Context, req *CallToolRequest, args map[string]any) (*CallToolResult, any, error) {
		return nil, nil, &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
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

		var wireErr *jsonrpc.Error
		if !errors.As(err, &wireErr) {
			t.Fatalf("expected jsonrpc.Error, got %[1]T: %[1]v", err)
		}

		if wireErr.Code != jsonrpc.CodeInvalidParams {
			t.Errorf("expected error code %d, got %d", jsonrpc.CodeInvalidParams, wireErr.Code)
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

// TestCallToolRaw verifies that ClientSession.CallToolRaw returns raw JSON
// content for both structured and unstructured tool results, normalizes
// nil/empty arguments to a JSON object, and surfaces tool errors via IsError
// rather than protocol errors.
func TestCallToolRaw(t *testing.T) {
	type echoIn struct {
		Msg string `json:"msg"`
	}
	type echoOut struct {
		Echo string `json:"echo"`
	}

	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "echo"}, func(_ context.Context, _ *CallToolRequest, in echoIn) (*CallToolResult, echoOut, error) {
		return nil, echoOut{Echo: in.Msg}, nil
	})
	AddTool(server, &Tool{Name: "boom"}, func(_ context.Context, _ *CallToolRequest, _ struct{}) (*CallToolResult, any, error) {
		return nil, nil, errors.New("tool failed")
	})

	ct, st := NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := NewClient(testImpl, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ctx := context.Background()

	t.Run("structured", func(t *testing.T) {
		got, err := cs.CallToolRaw(ctx, &CallToolParams{
			Name:      "echo",
			Arguments: map[string]any{"msg": "hello"},
		})
		if err != nil {
			t.Fatalf("CallToolRaw failed: %v", err)
		}
		if got.IsError {
			t.Errorf("unexpected IsError=true; content=%s", got.Content)
		}
		// StructuredContent should contain exactly the bytes the tool produced;
		// no decode/re-encode should round-trip through Go types.
		if want := `{"echo":"hello"}`; string(got.StructuredContent) != want {
			t.Errorf("StructuredContent = %s, want %s", got.StructuredContent, want)
		}
		if len(got.Content) == 0 || got.Content[0] != '[' {
			t.Errorf("Content = %q, want non-empty JSON array", got.Content)
		}
	})

	t.Run("raw_arguments", func(t *testing.T) {
		// Gateway-style use: pass raw JSON bytes through CallToolParams.Arguments
		// without remarshaling them.
		got, err := cs.CallToolRaw(ctx, &CallToolParams{
			Name:      "echo",
			Arguments: json.RawMessage(`{"msg":"raw"}`),
		})
		if err != nil {
			t.Fatalf("CallToolRaw failed: %v", err)
		}
		if want := `{"echo":"raw"}`; string(got.StructuredContent) != want {
			t.Errorf("StructuredContent = %s, want %s", got.StructuredContent, want)
		}
	})

	t.Run("nil_params", func(t *testing.T) {
		got, err := cs.CallToolRaw(ctx, nil)
		if err == nil {
			t.Fatalf("CallToolRaw(nil) succeeded; want error for missing tool name; result=%+v", got)
		}
	})

	t.Run("tool_error", func(t *testing.T) {
		got, err := cs.CallToolRaw(ctx, &CallToolParams{Name: "boom"})
		if err != nil {
			t.Fatalf("CallToolRaw failed: %v", err)
		}
		if !got.IsError {
			t.Errorf("IsError = false, want true")
		}
	})
}

// TestRawContent_MarshalVerbatim verifies that marshaling a []Content
// composed of *RawContent items reproduces the underlying raw bytes
// verbatim, enabling gateway-style splicing without typed re-encoding.
func TestRawContent_MarshalVerbatim(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"hello"},{"type":"image","data":"AAA=","mimeType":"image/png"}]`)

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}
	content := make([]Content, len(items))
	for i, b := range items {
		content[i] = &RawContent{Raw: b}
	}

	got, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal []Content: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("RawContent splice mismatch:\n got = %s\nwant = %s", got, raw)
	}

	t.Run("nil_raw", func(t *testing.T) {
		b, err := json.Marshal(&RawContent{})
		if err != nil {
			t.Fatalf("marshal empty RawContent: %v", err)
		}
		if string(b) != "null" {
			t.Errorf("empty RawContent = %s, want null", b)
		}
	})
}

// TestCallToolRaw_RunsSendingMiddleware locks in middleware parity between
// CallTool and CallToolRaw: a sending middleware registered on the client
// fires exactly once for each call regardless of which method is used.
func TestCallToolRaw_RunsSendingMiddleware(t *testing.T) {
	type out struct {
		N int `json:"n"`
	}
	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "n"}, func(_ context.Context, _ *CallToolRequest, _ struct{}) (*CallToolResult, out, error) {
		return nil, out{N: 1}, nil
	})

	ct, st := NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}

	var calls int
	c := NewClient(testImpl, nil)
	c.AddSendingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method == methodCallTool {
				calls++
			}
			return next(ctx, method, req)
		}
	})
	cs, err := c.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if _, err := cs.CallTool(context.Background(), &CallToolParams{Name: "n"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got, want := calls, 1; got != want {
		t.Fatalf("after CallTool, middleware fired %d times, want %d", got, want)
	}
	if _, err := cs.CallToolRaw(context.Background(), &CallToolParams{Name: "n"}); err != nil {
		t.Fatalf("CallToolRaw: %v", err)
	}
	if got, want := calls, 2; got != want {
		t.Fatalf("after CallToolRaw, middleware fired %d times, want %d", got, want)
	}
}

// TestCallToolRawPassthrough is the canonical "MCP gateway" example: an
// upstream tool result is forwarded through a gateway server using
// CallToolRaw inbound and RawContent for outbound splicing, so the
// downstream client observes byte-identical Content and StructuredContent
// without any typed re-encoding in the middle.
func TestCallToolRawPassthrough(t *testing.T) {
	type out struct {
		N int `json:"n"`
	}
	upstream := NewServer(&Implementation{Name: "upstream", Version: "v1"}, nil)
	AddTool(upstream, &Tool{Name: "n"}, func(_ context.Context, _ *CallToolRequest, _ struct{}) (*CallToolResult, out, error) {
		return nil, out{N: 7}, nil
	})

	uct, ust := NewInMemoryTransports()
	if _, err := upstream.Connect(context.Background(), ust, nil); err != nil {
		t.Fatal(err)
	}
	upstreamCS, err := NewClient(testImpl, nil).Connect(context.Background(), uct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamCS.Close()

	// Capture what upstream returns so we can assert byte-for-byte passthrough.
	upstreamRaw, err := upstreamCS.CallToolRaw(context.Background(), &CallToolParams{Name: "n"})
	if err != nil {
		t.Fatalf("upstream CallToolRaw: %v", err)
	}

	gateway := NewServer(&Implementation{Name: "gateway", Version: "v1"}, nil)
	gateway.AddTool(&Tool{Name: "n", InputSchema: &jsonschema.Schema{Type: "object"}}, func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
		raw, err := upstreamCS.CallToolRaw(ctx, &CallToolParams{
			Name:      req.Params.Name,
			Arguments: req.Params.Arguments,
		})
		if err != nil {
			return nil, err
		}
		// Shallow-parse the Content array once and wrap each item in
		// RawContent so the outbound marshal splices the bytes verbatim.
		var items []json.RawMessage
		if len(raw.Content) > 0 {
			if err := json.Unmarshal(raw.Content, &items); err != nil {
				return nil, err
			}
		}
		content := make([]Content, len(items))
		for i, b := range items {
			content[i] = &RawContent{Raw: b}
		}
		return &CallToolResult{
			Content:           content,
			StructuredContent: raw.StructuredContent,
			IsError:           raw.IsError,
		}, nil
	})

	gct, gst := NewInMemoryTransports()
	if _, err := gateway.Connect(context.Background(), gst, nil); err != nil {
		t.Fatal(err)
	}
	gatewayCS, err := NewClient(testImpl, nil).Connect(context.Background(), gct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gatewayCS.Close()

	got, err := gatewayCS.CallToolRaw(context.Background(), &CallToolParams{Name: "n"})
	if err != nil {
		t.Fatalf("gateway CallToolRaw: %v", err)
	}
	if got.IsError {
		t.Errorf("IsError = true, want false")
	}
	if string(got.StructuredContent) != string(upstreamRaw.StructuredContent) {
		t.Errorf("StructuredContent mismatch:\n got = %s\nwant = %s", got.StructuredContent, upstreamRaw.StructuredContent)
	}
	if string(got.Content) != string(upstreamRaw.Content) {
		t.Errorf("Content mismatch:\n got = %s\nwant = %s", got.Content, upstreamRaw.Content)
	}
}

func TestValidateToolName(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		validTests := []struct {
			label    string
			toolName string
		}{
			{"simple alphanumeric names", "getUser"},
			{"names with underscores", "get_user_profile"},
			{"names with dashes", "user-profile-update"},
			{"names with dots", "admin.tools.list"},
			{"mixed character names", "DATA_EXPORT_v2.1"},
			{"single character names", "a"},
			{"128 character names", strings.Repeat("a", 128)},
		}
		for _, test := range validTests {
			t.Run(test.label, func(t *testing.T) {
				if err := validateToolName(test.toolName); err != nil {
					t.Errorf("validateToolName(%q) = %v, want nil", test.toolName, err)
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		invalidTests := []struct {
			label             string
			toolName          string
			wantErrContaining string
		}{
			{"empty names", "", "tool name cannot be empty"},
			{"names longer than 128 characters", strings.Repeat("a", 129), "tool name exceeds maximum length of 128 characters (current: 129)"},
			{"names with spaces", "get user profile", `tool name contains invalid characters: " "`},
			{"names with commas", "get,user,profile", `tool name contains invalid characters: ","`},
			{"names with forward slashes", "user/profile/update", `tool name contains invalid characters: "/"`},
			{"names with other special chars", "user@domain.com", `tool name contains invalid characters: "@"`},
			{"names with multiple invalid chars", "user name@domain,com", `tool name contains invalid characters: " ", "@", ","`},
			{"names with unicode characters", "user-ñame", `tool name contains invalid characters: "ñ"`},
		}
		for _, test := range invalidTests {
			t.Run(test.label, func(t *testing.T) {
				if err := validateToolName(test.toolName); err == nil || !strings.Contains(err.Error(), test.wantErrContaining) {
					t.Errorf("validateToolName(%q) = %v, want error containing %q", test.toolName, err, test.wantErrContaining)
				}
			})
		}

	})

}
