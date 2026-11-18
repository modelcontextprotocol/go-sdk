// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// TestServerErrors validates that the server returns appropriate error codes
// for various invalid requests.
func TestServerErrors(t *testing.T) {
	ctx := context.Background()

	// Set up a server with tools, prompts, and resources for testing
	cs, _, cleanup := basicConnection(t, func(s *Server) {
		// Add a tool with required parameters
		type RequiredParams struct {
			Name string `json:"name" jsonschema:"the name is required"`
		}
		handler := func(ctx context.Context, req *CallToolRequest, args RequiredParams) (*CallToolResult, any, error) {
			return &CallToolResult{
				Content: []Content{&TextContent{Text: "success"}},
			}, nil, nil
		}
		AddTool(s, &Tool{Name: "validate", Description: "validates params"}, handler)

		// Add a prompt
		s.AddPrompt(codeReviewPrompt, codReviewPromptHandler)

		// Add a resource that returns ResourceNotFoundError
		s.AddResource(
			&Resource{URI: "file:///test.txt", Name: "test", MIMEType: "text/plain"},
			func(ctx context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
				return nil, ResourceNotFoundError(req.Params.URI)
			},
		)
	})
	defer cleanup()

	testCases := []struct {
		name         string
		executeCall  func() error
		expectedCode int64
	}{
		// Note: "missing required param" is tested separately below, because
		// input validation errors are returned as tool results with IsError=true
		// rather than JSON-RPC errors (see #450).
		{
			name: "unknown tool",
			executeCall: func() error {
				_, err := cs.CallTool(ctx, &CallToolParams{
					Name:      "nonexistent_tool",
					Arguments: map[string]any{},
				})
				return err
			},
			expectedCode: jsonrpc.CodeInvalidParams,
		},
		{
			name: "unknown prompt",
			executeCall: func() error {
				_, err := cs.GetPrompt(ctx, &GetPromptParams{
					Name:      "nonexistent_prompt",
					Arguments: map[string]string{},
				})
				return err
			},
			expectedCode: jsonrpc.CodeInvalidParams,
		},
		{
			name: "resource not found",
			executeCall: func() error {
				_, err := cs.ReadResource(ctx, &ReadResourceParams{
					URI: "file:///test.txt",
				})
				return err
			},
			expectedCode: CodeResourceNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.executeCall()
			if err == nil {
				t.Fatal("got nil error, want non-nil")
			}

			var rpcErr *jsonrpc.Error
			if !errors.As(err, &rpcErr) {
				t.Fatalf("got error type %T, want jsonrpc.Error: %v", err, err)
			}

			if rpcErr.Code != tc.expectedCode {
				t.Errorf("got error code %d, want %d", rpcErr.Code, tc.expectedCode)
			}

			if rpcErr.Message == "" {
				t.Error("got empty error message, want non-empty")
			}
		})
	}
}

// TestResourceNotFoundErrorCode verifies ResourceNotFoundError code and
// CodeResourceNotFound are -32602 (InvalidParams) per SEP-2164.
func TestResourceNotFoundErrorCode(t *testing.T) {
	err := ResourceNotFoundError("file:///test.txt")
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("got error type %T, want *jsonrpc.Error", err)
	}
	if rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("got error code %d, want %d (InvalidParams)", rpcErr.Code, jsonrpc.CodeInvalidParams)
	}
	if CodeResourceNotFound != jsonrpc.CodeInvalidParams {
		t.Errorf("CodeResourceNotFound = %d, want %d", CodeResourceNotFound, jsonrpc.CodeInvalidParams)
	}
}

// Go's %q escapes some characters (such as DEL or the tag characters in flag
// emoji) using sequences that are not valid JSON.
var unusualResourceURIs = []string{
	"file:///del\x7f.txt",
	"file:///\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F.txt",
}

func TestResourceNotFoundErrorData(t *testing.T) {
	for _, uri := range unusualResourceURIs {
		err := ResourceNotFoundError(uri)
		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("ResourceNotFoundError(%q): got error type %T, want *jsonrpc.Error", uri, err)
		}
		var data struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
			t.Fatalf("ResourceNotFoundError(%q): unmarshaling data %s: %v", uri, rpcErr.Data, err)
		}
		if data.URI != uri {
			t.Errorf("ResourceNotFoundError(%q): got data URI %q", uri, data.URI)
		}
	}
}

func TestReadUnregisteredResourceKeepsSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cs, _, cleanup := basicConnection(t, func(s *Server) {
		s.AddResource(
			&Resource{URI: "file:///test.txt", Name: "test", MIMEType: "text/plain"},
			func(ctx context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
				return &ReadResourceResult{}, nil
			},
		)
	})
	defer cleanup()

	for _, uri := range unusualResourceURIs {
		_, err := cs.ReadResource(ctx, &ReadResourceParams{URI: uri})
		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != CodeResourceNotFound {
			t.Fatalf("ReadResource(%q): got error %v, want resource not found", uri, err)
		}
		if _, err := cs.ListResources(ctx, nil); err != nil {
			t.Fatalf("ListResources after ReadResource(%q) failed: %v", uri, err)
		}
	}
}

// TestReadResourceNilResult verifies that a resource handler returning
// (nil, nil) yields a clean error to the client instead of a nil pointer
// panic that crashes the server. The sibling handlers callTool and getPrompt
// already tolerate a (nil, nil) return; readResource must do the same.
func TestReadResourceNilResult(t *testing.T) {
	ctx := context.Background()

	cs, _, cleanup := basicConnection(t, func(s *Server) {
		s.AddResource(
			&Resource{URI: "file:///nil.txt", Name: "nil", MIMEType: "text/plain"},
			func(ctx context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
				return nil, nil
			},
		)
	})
	defer cleanup()

	_, err := cs.ReadResource(ctx, &ReadResourceParams{URI: "file:///nil.txt"})
	if err == nil {
		t.Fatal("got nil error, want non-nil error for nil resource result")
	}
	if !strings.Contains(err.Error(), "nil information") {
		t.Errorf("got error %q, want it to mention 'nil information'", err.Error())
	}
}

// TestInputValidationToolError validates that input validation errors (missing
// required params, wrong types) are returned as tool results with IsError=true,
// not as JSON-RPC errors. This allows LLMs to see the error and self-correct.
// See #450.
func TestInputValidationToolError(t *testing.T) {
	ctx := context.Background()

	type RequiredParams struct {
		Name string `json:"name" jsonschema:"the name is required"`
	}
	handler := func(ctx context.Context, req *CallToolRequest, args RequiredParams) (*CallToolResult, any, error) {
		return &CallToolResult{
			Content: []Content{&TextContent{Text: "success"}},
		}, nil, nil
	}

	cs, _, cleanup := basicConnection(t, func(s *Server) {
		AddTool(s, &Tool{Name: "validate", Description: "validates params"}, handler)
	})
	defer cleanup()

	// Call the tool with missing required "name" field.
	result, err := cs.CallTool(ctx, &CallToolParams{
		Name:      "validate",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool returned error: %v; want tool result with IsError", err)
	}
	if !result.IsError {
		t.Fatal("got IsError=false, want IsError=true for missing required param")
	}
	text := result.Content[0].(*TextContent).Text
	if !strings.Contains(text, "name") {
		t.Errorf("error text %q does not mention missing field \"name\"", text)
	}
}

// TestURLElicitationRequired validates that URL elicitation required errors
// are properly created.
func TestURLElicitationRequired(t *testing.T) {
	t.Run("error creation", func(t *testing.T) {
		elicitations := []*ElicitParams{
			{
				Mode:          "url",
				Message:       "Please authorize",
				URL:           "https://example.com/auth",
				ElicitationID: "auth-123",
			},
		}

		err := URLElicitationRequiredError(elicitations)

		var rpcErr *jsonrpc.Error
		if !errors.As(err, &rpcErr) {
			t.Fatalf("got error type %T, want jsonrpc.Error", err)
		}

		if rpcErr.Code != CodeURLElicitationRequired {
			t.Errorf("got error code %d, want %d", rpcErr.Code, CodeURLElicitationRequired)
		}

		if rpcErr.Message != "URL elicitation required" {
			t.Errorf("got message %q, want 'URL elicitation required'", rpcErr.Message)
		}

		if rpcErr.Data == nil {
			t.Fatal("got nil error data, want non-nil")
		}

		// Verify the elicitations can be unmarshaled from the error data
		var errorData struct {
			Elicitations []*ElicitParams `json:"elicitations"`
		}
		if err := json.Unmarshal(rpcErr.Data, &errorData); err != nil {
			t.Fatalf("failed to unmarshal error data: %v", err)
		}

		if len(errorData.Elicitations) != 1 {
			t.Fatalf("got %d elicitations, want 1", len(errorData.Elicitations))
		}

		if errorData.Elicitations[0].URL != "https://example.com/auth" {
			t.Errorf("got URL %q, want 'https://example.com/auth'", errorData.Elicitations[0].URL)
		}
	})

	t.Run("error creation with non-URL mode panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("got no panic when creating URLElicitationRequiredError with non-URL mode, want panic")
			}
		}()

		// This should panic because mode is "form"
		URLElicitationRequiredError([]*ElicitParams{
			{
				Mode:          "form",
				Message:       "This should panic",
				ElicitationID: "bad-123",
			},
		})
	})

	t.Run("error creation with empty mode panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("got no panic when creating URLElicitationRequiredError with empty mode (defaults to form), want panic")
			}
		}()

		// This should panic because empty mode defaults to "form"
		URLElicitationRequiredError([]*ElicitParams{
			{
				Message:       "This should panic",
				ElicitationID: "bad-123",
			},
		})
	})
}

func TestParseURLElicitationRequiredError(t *testing.T) {
	want := []*ElicitParams{
		{Mode: "url", Message: "Sign in to A", URL: "https://a.example.com/auth", ElicitationID: "a-1"},
		{Mode: "url", Message: "Sign in to B", URL: "https://b.example.com/auth", ElicitationID: "b-1"},
	}

	t.Run("round trip through wrapping", func(t *testing.T) {
		err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", URLElicitationRequiredError(want)))
		got, ok, parseErr := ParseURLElicitationRequiredError(err)
		if !ok || parseErr != nil {
			t.Fatalf("got ok=%v, parseErr=%v; want ok=true, parseErr=nil", ok, parseErr)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("elicitations mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("returned by CallTool", func(t *testing.T) {
		cs, _, cleanup := basicConnection(t, func(s *Server) {
			AddTool(s, &Tool{Name: "needs-auth"}, func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, any, error) {
				return nil, nil, URLElicitationRequiredError(want)
			})
		})
		defer cleanup()

		_, err := cs.CallTool(t.Context(), &CallToolParams{Name: "needs-auth"})
		got, ok, parseErr := ParseURLElicitationRequiredError(err)
		if !ok || parseErr != nil {
			t.Fatalf("CallTool error %v: got ok=%v, parseErr=%v; want ok=true, parseErr=nil", err, ok, parseErr)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("elicitations mismatch (-want +got):\n%s", diff)
		}
	})

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"plain error", errors.New("boom")},
		{"other code", &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "bad"}},
	} {
		t.Run("not ok/"+tc.name, func(t *testing.T) {
			got, ok, parseErr := ParseURLElicitationRequiredError(tc.err)
			if got != nil || ok || parseErr != nil {
				t.Errorf("got (%v, %v, %v), want (nil, false, nil)", got, ok, parseErr)
			}
		})
	}

	for _, tc := range []struct {
		name string
		data string
	}{
		{"no data", ""},
		{"invalid json", `{"elicitations":`},
		{"no elicitations", `{}`},
		{"empty elicitations", `{"elicitations":[]}`},
		{"null elicitation", `{"elicitations":[null]}`},
		{"form mode", `{"elicitations":[{"mode":"form","message":"m","url":"https://x","elicitationId":"1"}]}`},
		{"empty mode", `{"elicitations":[{"message":"m","url":"https://x","elicitationId":"1"}]}`},
		{"no url", `{"elicitations":[{"mode":"url","message":"m","elicitationId":"1"}]}`},
		{"no elicitation id", `{"elicitations":[{"mode":"url","message":"m","url":"https://x"}]}`},
		{"valid then invalid", `{"elicitations":[{"mode":"url","message":"m","url":"https://x","elicitationId":"1"},{"mode":"form","message":"m"}]}`},
	} {
		t.Run("malformed/"+tc.name, func(t *testing.T) {
			err := &jsonrpc.Error{Code: CodeURLElicitationRequired, Message: "URL elicitation required"}
			if tc.data != "" {
				err.Data = json.RawMessage(tc.data)
			}
			got, ok, parseErr := ParseURLElicitationRequiredError(err)
			if got != nil || !ok || parseErr == nil {
				t.Errorf("got (%v, %v, %v), want (nil, true, non-nil)", got, ok, parseErr)
			}
		})
	}

	t.Run("malformed/reports invalid index", func(t *testing.T) {
		err := &jsonrpc.Error{
			Code: CodeURLElicitationRequired,
			Data: json.RawMessage(`{"elicitations":[{"mode":"url","message":"m","url":"https://x","elicitationId":"1"},{"mode":"url","message":"m","elicitationId":"2"}]}`),
		}
		_, _, parseErr := ParseURLElicitationRequiredError(err)
		if parseErr == nil || !strings.Contains(parseErr.Error(), "elicitation 1 ") {
			t.Errorf("got parseErr %v, want it to name elicitation 1", parseErr)
		}
	})
}
