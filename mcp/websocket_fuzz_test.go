// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build go1.18
// +build go1.18

package mcp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// FuzzWebSocketRead fuzzes the WebSocket Read method to test JSON decoding robustness.
// This helps ensure the implementation handles malformed or unexpected JSON-RPC messages gracefully.
func FuzzWebSocketRead(f *testing.F) {
	// Seed corpus with valid and invalid JSON-RPC messages
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"test","params":{}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":"success"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`))
	f.Add([]byte(`{`))                                 // Truncated JSON
	f.Add([]byte(`{"jsonrpc":"2.0"}`))                 // Missing required fields
	f.Add([]byte(`null`))                              // Null value
	f.Add([]byte(`[]`))                                // Array instead of object
	f.Add([]byte(`{"jsonrpc":"2.0","id":"string"}`))   // String ID
	f.Add([]byte(`{"jsonrpc":"1.0","id":1}`))          // Wrong version
	f.Add([]byte(strings.Repeat("a", 10000)))          // Large payload
	f.Add([]byte(`{"jsonrpc":"2.0","id":null}`))       // Null ID
	f.Add([]byte(`{"jsonrpc":"2.0","method":"test"}`)) // Notification

	f.Fuzz(func(t *testing.T, data []byte) {
		// Skip empty input
		if len(data) == 0 {
			t.Skip()
		}

		// Create a test WebSocket server that sends the fuzzed data
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				Subprotocols: []string{"mcp"},
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			// Send the fuzzed data as a text message
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}

			// Keep connection open briefly
			time.Sleep(50 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		transport := &WebSocketClientTransport{
			URL: wsURL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, err := transport.Connect(ctx)
		if err != nil {
			// Connection failure is acceptable in fuzzing
			return
		}
		defer conn.Close()

		// Try to read the fuzzed message
		// Should never panic, but may return an error
		_, err = conn.Read(ctx)
		// Error is acceptable - we're testing that it doesn't panic
		_ = err
	})
}

// FuzzWebSocketMessageDecoding fuzzes JSON-RPC message decoding directly.
func FuzzWebSocketMessageDecoding(f *testing.F) {
	// Seed corpus with various JSON-RPC message types
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":2,"result":{"capabilities":{}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":3,"error":{"code":-32700,"message":"Parse error"}}`))
	f.Add([]byte(`{invalid json`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":[],"method":"test"}`)) // Invalid ID type

	f.Fuzz(func(t *testing.T, data []byte) {
		// Skip empty input
		if len(data) == 0 {
			t.Skip()
		}

		// Attempt to decode - should never panic
		_, err := jsonrpc.DecodeMessage(data)
		// Error is acceptable, panic is not
		_ = err
	})
}

// FuzzWebSocketURL fuzzes WebSocket URL handling.
func FuzzWebSocketURL(f *testing.F) {
	// Seed corpus with various URL formats
	f.Add("ws://localhost:8080/mcp")
	f.Add("wss://example.com/mcp")
	f.Add("ws://localhost")
	f.Add("ws://127.0.0.1:9999")
	f.Add("wss://example.com:443/path/to/mcp?query=value")
	f.Add("invalid://url")
	f.Add("")
	f.Add("http://example.com") // Wrong scheme

	f.Fuzz(func(t *testing.T, url string) {
		// Skip empty URLs
		if url == "" {
			t.Skip()
		}

		transport := &WebSocketClientTransport{
			URL: url,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		// Try to connect - should never panic
		conn, err := transport.Connect(ctx)
		if err == nil && conn != nil {
			conn.Close()
		}
		// Error is acceptable, panic is not
	})
}

// FuzzWebSocketHeaders fuzzes custom HTTP header handling.
func FuzzWebSocketHeaders(f *testing.F) {
	// Seed corpus with various header combinations
	f.Add("X-Custom-Header", "value")
	f.Add("Authorization", "Bearer token123")
	f.Add("", "")
	f.Add("Content-Type", "application/json")
	f.Add(strings.Repeat("X", 1000), "value") // Long header name
	f.Add("Key", strings.Repeat("v", 10000))  // Long header value

	f.Fuzz(func(t *testing.T, headerName, headerValue string) {
		// Create server that checks headers
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				Subprotocols: []string{"mcp"},
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			conn.Close()
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		headers := http.Header{}
		if headerName != "" {
			headers.Add(headerName, headerValue)
		}

		transport := &WebSocketClientTransport{
			URL:    wsURL,
			Header: headers,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		// Try to connect - should never panic
		conn, err := transport.Connect(ctx)
		if err == nil && conn != nil {
			conn.Close()
		}
		// Error is acceptable, panic is not
	})
}

// FuzzWebSocketBinaryMessages fuzzes handling of binary WebSocket messages.
func FuzzWebSocketBinaryMessages(f *testing.F) {
	// Seed corpus with various binary data
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})
	f.Add([]byte{0xFF, 0xFE, 0xFD})
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0x41}, 1000)) // Repeated 'A'

	f.Fuzz(func(t *testing.T, data []byte) {
		// Create server that sends binary message
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				Subprotocols: []string{"mcp"},
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()

			// Send binary message (which should be rejected)
			conn.WriteMessage(websocket.BinaryMessage, data)
			time.Sleep(50 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		transport := &WebSocketClientTransport{
			URL: wsURL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		conn, err := transport.Connect(ctx)
		if err != nil {
			return
		}
		defer conn.Close()

		// Try to read - should return error for binary message, not panic
		_, err = conn.Read(ctx)
		if err == nil {
			t.Error("Expected error for binary message")
		}
	})
}
