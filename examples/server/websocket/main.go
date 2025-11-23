// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EchoParams defines the parameters for the echo tool
type EchoParams struct {
	Text string `json:"text"`
}

// EchoTool echoes back the input text
func EchoTool(ctx context.Context, req *mcp.CallToolRequest, args EchoParams) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Echo: %s", args.Text)},
		},
	}, nil, nil
}

func main() {
	// Create a new MCP server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "websocket-example-server",
		Version: "1.0.0",
	}, nil)

	// Add the echo tool
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes back the input text",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"text": map[string]interface{}{
					"type":        "string",
					"description": "The text to echo back",
				},
			},
			"required": []string{"text"},
		},
	}, EchoTool)

	// Create WebSocket upgrader
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"mcp"},
		CheckOrigin: func(r *http.Request) bool {
			// Allow all origins for this example
			// In production, implement proper origin checking
			return true
		},
	}

	// Create HTTP handler that upgrades to WebSocket and serves MCP
	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		// Upgrade HTTP connection to WebSocket
		wsConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket upgrade failed: %v", err)
			return
		}
		defer wsConn.Close()

		log.Printf("Client connected from %s", r.RemoteAddr)

		// Create a custom transport that returns our WebSocket connection
		transport := &websocketServerTransport{conn: wsConn}

		// Run the server on this transport
		ctx := context.Background()
		if err := server.Run(ctx, transport); err != nil {
			log.Printf("Server error: %v", err)
		}

		log.Printf("Client disconnected from %s", r.RemoteAddr)
	})

	// Serve a simple info page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
<!DOCTYPE html>
<html>
<head>
    <title>MCP WebSocket Server</title>
</head>
<body>
    <h1>MCP WebSocket Server</h1>
    <p>This server provides MCP over WebSocket.</p>
    <p>Connect to <code>ws://localhost:8080/mcp</code> using the MCP WebSocket client.</p>
    <h2>Available Tools:</h2>
    <ul>
        <li><strong>echo</strong>: Echoes back the input text</li>
    </ul>
    <h2>Available Prompts:</h2>
    <ul>
        <li><strong>greeting</strong>: A friendly greeting</li>
    </ul>
</body>
</html>
`)
	})

	addr := ":8080"
	log.Printf("Starting MCP WebSocket server on %s", addr)
	log.Printf("Connect to ws://localhost:8080/mcp")
	log.Printf("Visit http://localhost:8080 for information")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// websocketConnection wraps a gorilla/websocket.Conn to implement mcp.Connection
type websocketConnection struct {
	conn *websocket.Conn
}

func (c *websocketConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	messageType, data, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if messageType != websocket.TextMessage {
		return nil, fmt.Errorf("expected text message, got %d", messageType)
	}
	// Decode the JSON-RPC message
	msg, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON-RPC message: %w", err)
	}
	return msg, nil
}

func (c *websocketConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	// Encode the message
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to encode JSON-RPC message: %w", err)
	}
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *websocketConnection) Close() error {
	return c.conn.Close()
}

func (c *websocketConnection) SessionID() string {
	return c.conn.RemoteAddr().String()
}

// websocketServerTransport wraps a websocket connection to act as a Transport for the server
type websocketServerTransport struct {
	conn *websocket.Conn
}

func (t *websocketServerTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	return &websocketConnection{conn: t.conn}, nil
}
