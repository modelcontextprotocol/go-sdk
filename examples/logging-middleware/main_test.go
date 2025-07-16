// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setupTestLogging creates a temporary log file and returns a logger and
// cleanup function
func setupTestLogging(t *testing.T) (*slog.Logger, func() []string, func()) {
	t.Helper()

	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "test.log")

	file, err := os.Create(logFile)
	if err != nil {
		t.Fatalf("Failed to create test log file: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String("timestamp", a.Value.Time().Format(time.RFC3339))
			}
			return a
		},
	}))

	readLogs := func() []string {
		file.Close()

		content, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("Failed to read log file: %v", err)
		}

		var lines []string
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				lines = append(lines, line)
			}
		}
		return lines
	}

	cleanup := func() {
		file.Close()
		os.Remove(logFile)
	}

	return logger, readLogs, cleanup
}

// logContains checks if any log entry contains the specified message
func logContains(logs []string, message string) bool {
	for _, log := range logs {
		if strings.Contains(log, message) {
			return true
		}
	}
	return false
}

// parseLogLevel extracts the level from a JSON log entry
func parseLogLevel(logLine string) string {
	var entry map[string]any
	if err := json.Unmarshal([]byte(logLine), &entry); err != nil {
		return ""
	}
	if level, ok := entry["level"].(string); ok {
		return level
	}
	return ""
}

// setupInMemorySession creates a client-server pair using in-memory transport
func setupInMemorySession(t *testing.T, logger *slog.Logger) (*mcp.ClientSession, func()) {
	t.Helper()

	server := createServer(logger)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil)

	// Create in-memory transports
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx := context.Background()

	// Connect server
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		t.Fatalf("Failed to connect server: %v", err)
	}

	// Connect client
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}

	cleanup := func() {
		clientSession.Close()
		serverSession.Close()
	}

	return clientSession, cleanup
}

func TestMCPMiddlewareLogging(t *testing.T) {
	logger, readLogs, cleanup := setupTestLogging(t)
	defer cleanup()

	session, cleanupSession := setupInMemorySession(t, logger)
	defer cleanupSession()

	ctx := context.Background()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "greet",
		Arguments: map[string]any{
			"name": "TestUser",
		},
	})
	if err != nil {
		t.Fatalf("Failed to call greet tool: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("Expected content in tool result")
	}

	logs := readLogs()

	if !logContains(logs, "MCP method started") {
		t.Error("Expected 'MCP method started' log entry")
	}

	if !logContains(logs, "MCP method completed") {
		t.Error("Expected 'MCP method completed' log entry")
	}

	if !logContains(logs, "initialize") {
		t.Error("Expected 'initialize' method in logs")
	}

	if !logContains(logs, "tools/call") {
		t.Error("Expected 'tools/call' method in logs")
	}

	if !logContains(logs, "greet tool executed") {
		t.Error("Expected tool-specific logging")
	}

	t.Logf("Captured %d log entries", len(logs))
}

func TestErrorLoggingViaMiddleware(t *testing.T) {
	logger, readLogs, cleanup := setupTestLogging(t)
	defer cleanup()

	session, cleanupSession := setupInMemorySession(t, logger)
	defer cleanupSession()

	ctx := context.Background()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "error_demo",
		Arguments: map[string]any{
			"should_error": true,
		},
	})

	if err == nil {
		t.Error("Expected an error from error_demo tool")
	} else if !strings.Contains(err.Error(), "demonstration error as requested") {
		t.Errorf("Expected error message not found in: %v", err)
	}

	if result != nil {
		t.Error("Expected nil result when tool returns an error")
	}

	logs := readLogs()

	if !logContains(logs, "MCP method failed") {
		t.Error("Expected 'MCP method failed' log entry")
	}

	if !logContains(logs, "demonstration error as requested") {
		t.Error("Expected error message in logs")
	}

	hasErrorLevel := false
	for _, log := range logs {
		if parseLogLevel(log) == "ERROR" {
			hasErrorLevel = true
			break
		}
	}
	if !hasErrorLevel {
		t.Error("Expected at least one ERROR level log entry")
	}

	t.Logf("Error test captured %d log entries", len(logs))
}

func TestListToolsWithLogging(t *testing.T) {
	logger, readLogs, cleanup := setupTestLogging(t)
	defer cleanup()

	session, cleanupSession := setupInMemorySession(t, logger)
	defer cleanupSession()

	ctx := context.Background()

	result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	if len(result.Tools) != 2 {
		t.Errorf("Expected 2 tools, got %d", len(result.Tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"greet", "error_demo"}
	for _, expectedTool := range expectedTools {
		if !toolNames[expectedTool] {
			t.Errorf("Missing expected tool: %s", expectedTool)
		}
	}

	logs := readLogs()

	if !logContains(logs, "tools/list") {
		t.Error("Expected 'tools/list' method in logs")
	}

	t.Logf("ListTools test captured %d log entries", len(logs))
}
