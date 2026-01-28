// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// The conformance client implements features required for MCP conformance testing.
// It mirrors the functionality of the TypeScript conformance client at
// https://github.com/modelcontextprotocol/typescript-sdk/blob/main/src/conformance/everything-client.ts
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// scenarioHandler is the function signature for all conformance test scenarios.
// It takes a context and the server URL to connect to.
type scenarioHandler func(ctx context.Context, serverURL string) error

var (
	// registry stores all registered scenario handlers.
	registry = make(map[string]scenarioHandler)
)

// registerScenario registers a new scenario handler with the given name.
// This function should be called during init() by scenario implementations.
func registerScenario(name string, handler scenarioHandler) {
	if _, exists := registry[name]; exists {
		log.Fatalf("Scenario %q is already registered", name)
	}
	registry[name] = handler
}

func init() {
	registerScenario("initialize", runBasicClient)
	registerScenario("tools_call", runToolsCallClient)
	registerScenario("elicitation-sep1034-client-defaults", runElicitationDefaultsClient)
	registerScenario("sse-retry", runSSERetryClient)
}

// ============================================================================
// Basic scenarios
// ============================================================================

func runBasicClient(ctx context.Context, serverURL string) error {
	session, err := connectToServer(ctx, serverURL)
	if err != nil {
		return err
	}
	defer session.Close()

	_, err = session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("session.ListTools(): %v", err)
	}

	return nil
}

func runToolsCallClient(ctx context.Context, serverURL string) error {
	session, err := connectToServer(ctx, serverURL)
	if err != nil {
		return err
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("session.ListTools(): %v", err)
	}

	idx := slices.IndexFunc(tools.Tools, func(t *mcp.Tool) bool {
		return t.Name == "add_numbers"
	})
	if idx == -1 {
		return fmt.Errorf("tool %q not found", "add_numbers")
	}

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_numbers",
		Arguments: map[string]any{"a": 5, "b": 3},
	})
	if err != nil {
		return fmt.Errorf("session.CallTool('add_numbers'): %v", err)
	}

	return nil
}

// ============================================================================
// Elicitation scenarios
// ============================================================================

func runElicitationDefaultsClient(ctx context.Context, serverURL string) error {
	elicitationHandler := func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return &mcp.ElicitResult{
			Action:  "accept",
			Content: map[string]any{},
		}, nil
	}

	session, err := connectToServer(ctx, serverURL, withClientOptions(&mcp.ClientOptions{
		ElicitationHandler: elicitationHandler,
	}))
	if err != nil {
		return err
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("session.ListTools(): %v", err)
	}

	var testToolName = "test_client_elicitation_defaults"
	idx := slices.IndexFunc(tools.Tools, func(t *mcp.Tool) bool {
		return t.Name == testToolName
	})
	if idx == -1 {
		return fmt.Errorf("tool %q not found", testToolName)
	}

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      testToolName,
		Arguments: map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("session.CallTool(%q): %v", testToolName, err)
	}

	return nil
}

// ============================================================================
// SSE retry scenario
// ============================================================================

func runSSERetryClient(ctx context.Context, serverURL string) error {
	// TODO: this scenario is not passing yet. It requires a fix in the client SSE handling.
	session, err := connectToServer(ctx, serverURL)
	if err != nil {
		return err
	}
	defer session.Close()
	log.Printf("Connected to server %q", serverURL)

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("session.ListTools(): %v", err)
	}

	var testToolName = "test_reconnection"
	idx := slices.IndexFunc(tools.Tools, func(t *mcp.Tool) bool {
		return t.Name == testToolName
	})
	if idx == -1 {
		return fmt.Errorf("tool %q not found", testToolName)
	}

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      testToolName,
		Arguments: map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("session.CallTool(%q): %v", testToolName, err)
	}

	return nil
}

// ============================================================================
// Main entry point
// ============================================================================

func main() {
	if len(os.Args) != 2 {
		printUsageAndExit("Usage: %s <server-url>", os.Args[0])
	}

	serverURL := os.Args[1]
	scenarioName := os.Getenv("MCP_CONFORMANCE_SCENARIO")

	if scenarioName == "" {
		printUsageAndExit("MCP_CONFORMANCE_SCENARIO not set")
	}

	handler, ok := registry[scenarioName]
	if !ok {
		printUsageAndExit("Unknown scenario: %q", scenarioName)
	}

	ctx := context.Background()
	if err := handler(ctx, serverURL); err != nil {
		log.Fatalf("Scenario %q failed: %v", scenarioName, err)
	}
}

func printUsageAndExit(format string, args ...any) {
	var scenarios []string
	for name := range registry {
		scenarios = append(scenarios, name)
	}
	sort.Strings(scenarios)

	msg := fmt.Sprintf(format, args...)
	log.Fatalf("%s\nAvailable scenarios:\n  - %s", msg, strings.Join(scenarios, "\n  - "))
}

type connectConfig struct {
	clientOptions *mcp.ClientOptions
}

type connectOption func(*connectConfig)

func withClientOptions(opts *mcp.ClientOptions) connectOption {
	return func(c *connectConfig) {
		c.clientOptions = opts
	}
}

// connectToServer connects to the MCP server and returns a client session.
// The caller is responsible for closing the session.
func connectToServer(ctx context.Context, serverURL string, opts ...connectOption) (*mcp.ClientSession, error) {
	config := &connectConfig{}
	for _, opt := range opts {
		opt(config)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "1.0.0",
	}, config.clientOptions)

	transport := &mcp.StreamableClientTransport{Endpoint: serverURL}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("client.Connect(): %v", err)
	}

	return session, nil
}
