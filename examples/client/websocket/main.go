// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()

	// Create WebSocket transport pointing to the server
	transport := &mcp.WebSocketClientTransport{
		URL: "ws://localhost:8080/mcp",
	}

	// Create MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "websocket-example-client",
		Version: "1.0.0",
	}, nil)

	// Connect to the server
	fmt.Println("Connecting to WebSocket MCP server at ws://localhost:8080/mcp...")
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer session.Close()

	fmt.Println("Connected successfully!")

	// Get server info from initialization
	initResult := session.InitializeResult()
	fmt.Printf("Connected to server: %s v%s\n", initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// List available tools
	fmt.Println("\nAvailable tools:")
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			log.Fatalf("Failed to list tools: %v", err)
		}
		fmt.Printf("  - %s: %s\n", tool.Name, tool.Description)
	}

	// Call the echo tool
	fmt.Println("\nCalling echo tool...")
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "echo",
		Arguments: map[string]any{
			"text": "Hello from WebSocket client!",
		},
	})
	if err != nil {
		log.Fatalf("Failed to call tool: %v", err)
	}

	fmt.Println("\nTool result:")
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			fmt.Printf("  %s\n", textContent.Text)
		}
	}

	fmt.Println("\nDisconnecting...")
}
