// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This example demonstrates server side logging using the mcp.Middleware system.
func Example_loggingMiddleware() {
	// Create a logger for demonstration purposes.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// Simplify timestamp format for consistent output.
			if a.Key == slog.TimeKey {
				return slog.String("time", "2025-01-01T00:00:00Z")
			}
			return a
		},
	}))

	loggingMiddleware := func(next mcp.MethodHandler[*mcp.ServerSession]) mcp.MethodHandler[*mcp.ServerSession] {
		return func(
			ctx context.Context,
			session *mcp.ServerSession,
			method string,
			params mcp.Params,
		) (mcp.Result, error) {
			logger.Info("MCP method started",
				"method", method,
				"session_id", session.ID(),
				"has_params", params != nil,
			)

			start := time.Now()

			result, err := next(ctx, session, method, params)

			duration := time.Since(start)

			if err != nil {
				logger.Error("MCP method failed",
					"method", method,
					"session_id", session.ID(),
					"duration_ms", duration.Milliseconds(),
					"err", err,
				)
			} else {
				logger.Info("MCP method completed",
					"method", method,
					"session_id", session.ID(),
					"duration_ms", duration.Milliseconds(),
					"has_result", result != nil,
				)
			}

			return result, err
		}
	}

	// Create server with middleware
	server := mcp.NewServer(&mcp.Implementation{Name: "logging-example"}, nil)
	server.AddReceivingMiddleware(loggingMiddleware)

	// Add a simple tool
	server.AddTool(
		&mcp.Tool{
			Name:        "greet",
			Description: "Greet someone with logging.",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {
						Type:        "string",
						Description: "Name to greet",
					},
				},
				Required: []string{"name"},
			},
		},
		func(
			ctx context.Context,
			ss *mcp.ServerSession,
			params *mcp.CallToolParamsFor[map[string]any],
		) (*mcp.CallToolResultFor[any], error) {
			name, ok := params.Arguments["name"].(string)
			if !ok {
				return nil, fmt.Errorf("name parameter is required and must be a string")
			}

			message := fmt.Sprintf("Hello, %s!", name)
			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{
					&mcp.TextContent{Text: message},
				},
			}, nil
		},
	)

	// Create client-server connection for demonstration
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx := context.Background()

	// Connect server and client
	serverSession, _ := server.Connect(ctx, serverTransport)
	defer serverSession.Close()

	clientSession, _ := client.Connect(ctx, clientTransport)
	defer clientSession.Close()

	// Call the tool to demonstrate logging
	result, _ := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "greet",
		Arguments: map[string]any{
			"name": "World",
		},
	})

	fmt.Printf("Tool result: %s\n", result.Content[0].(*mcp.TextContent).Text)

	// Output:
	// time=2025-01-01T00:00:00Z level=INFO msg="MCP method started" method=initialize session_id="" has_params=true
	// time=2025-01-01T00:00:00Z level=INFO msg="MCP method completed" method=initialize session_id="" duration_ms=0 has_result=true
	// time=2025-01-01T00:00:00Z level=INFO msg="MCP method started" method=notifications/initialized session_id="" has_params=true
	// time=2025-01-01T00:00:00Z level=INFO msg="MCP method completed" method=notifications/initialized session_id="" duration_ms=0 has_result=false
	// time=2025-01-01T00:00:00Z level=INFO msg="MCP method started" method=tools/call session_id="" has_params=true
	// time=2025-01-01T00:00:00Z level=INFO msg="MCP method completed" method=tools/call session_id="" duration_ms=0 has_result=true
	// Tool result: Hello, World!
}
