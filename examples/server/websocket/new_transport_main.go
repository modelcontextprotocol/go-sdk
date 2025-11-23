// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "websocket-transport-server", Version: "1.0.0"}, nil)

	// Minimal tool used by the example
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Simple ping tool that returns a pong message",
		InputSchema: map[string]interface{}{
			"type": "object",
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, args any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil, nil
	})

	// Create a server transport using the SDK-provided WebSocket handler.
	// This example demonstrates the 'NewWebSocketServerTransport' usage where
	// the handler performs the upgrade and connects to an mcp.Server.
	wsTransport := mcp.NewWebSocketServerTransport(func(r *http.Request) *mcp.Server {
		// You can inspect 'r' for authentication/headers and return 'nil' to
		// reject the connection (which will result in a 404).
		return server
	})

	// Optionally set CheckOrigin for production
	wsTransport.CheckOrigin = func(r *http.Request) bool {
		// Return true only for same origin requests in a real app
		return true
	}

	http.Handle("/mcp", wsTransport)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><body><h1>MCP WebSocket Server using NewWebSocketServerTransport</h1></body></html>")
	})

	addr := ":8081"
	log.Printf("Starting WebSocket server using NewWebSocketServerTransport on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
