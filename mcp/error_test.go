// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"testing"

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
		{
			name: "missing required param",
			executeCall: func() error {
				_, err := cs.CallTool(ctx, &CallToolParams{
					Name:      "validate",
					Arguments: map[string]any{}, // Missing required "name" field
				})
				return err
			},
			expectedCode: jsonrpc.CodeInvalidParams,
		},
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
				t.Fatal("expected error, got nil")
			}

			var rpcErr *jsonrpc.Error
			if !errors.As(err, &rpcErr) {
				t.Fatalf("expected jsonrpc.Error, got %T: %v", err, err)
			}

			if rpcErr.Code != tc.expectedCode {
				t.Errorf("expected error code %d, got %d", tc.expectedCode, rpcErr.Code)
			}

			if rpcErr.Message == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}
