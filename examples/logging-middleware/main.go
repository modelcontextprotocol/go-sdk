// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// logging-middleware demonstrates a server with logging. This example shows
// how to add detailed observability to MCP protocol operations using
// middleware patterns defined in the MCP Go SDK design.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String(
	"http",
	"",
	"if set, use streamable HTTP at this address, instead of stdin/stdout",
)

// LoggingMiddleware provides MCP-level logging.
func LoggingMiddleware(logger *slog.Logger) mcp.Middleware[*mcp.ServerSession] {
	return func(next mcp.MethodHandler[*mcp.ServerSession]) mcp.MethodHandler[*mcp.ServerSession] {
		return func(
			ctx context.Context,
			session *mcp.ServerSession,
			method string,
			params mcp.Params,
		) (mcp.Result, error) {
			start := time.Now()

			// Note: The actual HTTP session ID (from Mcp-Session-Id header) is not
			// accessible from middleware with the current SDK design. This would require
			// SDK changes to pass the session ID through context. For now, we use the
			// session pointer as a unique identifier within this process.
			sessionID := fmt.Sprintf("%p", session)

			logger.Info("MCP method started",
				"method", method,
				"session_id", sessionID,
				"has_params", params != nil,
			)

			// Log method parameters (be careful with sensitive data)
			if params != nil {
				logger.Debug("MCP method params",
					"method", method,
					"session_id", sessionID,
					"params_type", fmt.Sprintf("%T", params),
				)
			}

			// Call the actual handler
			result, err := next(ctx, session, method, params)

			duration := time.Since(start)

			if err != nil {
				logger.Error("MCP method failed",
					"method", method,
					"session_id", sessionID,
					"duration_ms", duration.Milliseconds(),
					"error", err.Error(),
				)
			} else {
				logger.Info("MCP method completed",
					"method", method,
					"session_id", sessionID,
					"duration_ms", duration.Milliseconds(),
					"has_result", result != nil,
				)

				if result != nil {
					logger.Debug("MCP method result",
						"method", method,
						"session_id", sessionID,
						"result_type", fmt.Sprintf("%T", result),
					)
				}
			}

			return result, err
		}
	}
}

// createServer creates a new MCP server with logging middleware.
func createServer(logger *slog.Logger) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "logging-middleware-example"}, nil)

	// Add logging middleware to the server
	server.AddReceivingMiddleware(LoggingMiddleware(logger))

	// Add a simple greeting tool
	server.AddTool(
		&mcp.Tool{
			Name:        "greet",
			Description: "Greet someone with logging",
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
			// Extract name from untyped arguments
			name, ok := params.Arguments["name"].(string)
			if !ok {
				return nil, fmt.Errorf("name parameter is required and must be a string")
			}

			message := fmt.Sprintf("Hello, %s! This greeting was logged via MCP middleware.", name)

			// Additional tool-specific logging
			logger.Info("greet tool executed",
				"name", name,
				"message_length", len(message),
			)

			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{
					&mcp.TextContent{Text: message},
				},
			}, nil
		},
	)

	// Add a tool that demonstrates error logging
	server.AddTool(
		&mcp.Tool{
			Name:        "error_demo",
			Description: "Demonstrate error logging via middleware",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"should_error": {
						Type:        "boolean",
						Description: "Whether to return an error",
					},
				},
				Required: []string{"should_error"},
			},
		},
		func(
			ctx context.Context,
			ss *mcp.ServerSession,
			params *mcp.CallToolParamsFor[map[string]any],
		) (*mcp.CallToolResultFor[any], error) {
			// Extract should_error from untyped arguments
			shouldError, ok := params.Arguments["should_error"].(bool)
			if !ok {
				return nil, fmt.Errorf("should_error parameter is required and must be a boolean")
			}

			if shouldError {
				return nil, fmt.Errorf("demonstration error as requested")
			}

			return &mcp.CallToolResultFor[any]{
				Content: []mcp.Content{
					&mcp.TextContent{Text: "No error occurred"},
				},
			}, nil
		},
	)

	logger.Info("MCP server configured with logging middleware",
		"tools_count", 2,
	)

	return server
}

func main() {
	flag.Parse()

	logFile, err := os.OpenFile("server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String("timestamp", a.Value.Time().Format(time.RFC3339))
			}
			return a
		},
	}))

	server := createServer(logger)

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("MCP handler listening at %s", *httpAddr)
		http.ListenAndServe(*httpAddr, handler)
	} else {
		t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
		if err := server.Run(context.Background(), t); err != nil {
			log.Printf("Server failed: %v", err)
		}
	}
}
