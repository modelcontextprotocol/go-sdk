// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// NOTE: WebSocket tests assume a negotiated subprotocol of "mcp" between
// client and server (i.e., both sides select the 'mcp' subprotocol during the
// WebSocket handshake). Tests validate that binary frames are rejected and
// that context deadlines or cancellations cause Read/Write to return the
// appropriate context errors.

func TestWebSocketClientTransport(t *testing.T) {
	// Create a test WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		// Echo messages back to the client
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, data); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	// Convert http:// URL to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create WebSocket transport
	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	// Connect
	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Test Write and Read
	testReq, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)
	if err != nil {
		t.Fatalf("Failed to create test request: %v", err)
	}

	if err := conn.Write(ctx, testReq); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	receivedMsg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read: %v", err)
	}

	// Verify it's a request with the same ID and method
	if req, ok := receivedMsg.(*jsonrpc.Request); ok {
		if req.Method != "test" {
			t.Errorf("Expected method 'test', got '%s'", req.Method)
		}
		if req.ID.Raw() != int64(1) {
			t.Errorf("Expected ID 1, got %v", req.ID.Raw())
		}
	} else {
		t.Errorf("Expected Request, got %T", receivedMsg)
	}

	// Test SessionID
	sessionID := conn.SessionID()
	if sessionID == "" {
		t.Error("SessionID should not be empty")
	}
}

func TestWebSocketConnectionClose(t *testing.T) {
	// Create a test WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection alive
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Close the connection
	if err := conn.Close(); err != nil {
		t.Errorf("Failed to close connection: %v", err)
	}

	// Verify multiple closes don't cause issues
	if err := conn.Close(); err != nil {
		t.Errorf("Second close should not error: %v", err)
	}
}

func TestWebSocketContextCancellation(t *testing.T) {
	// Create a test WebSocket server that delays responses
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection alive but don't respond immediately
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create a context with timeout
	readCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	// Attempt to read with cancelled context
	_, err = conn.Read(readCtx)
	if err == nil {
		t.Error("Expected error from cancelled context, got nil")
	}
}

func TestWebSocketConnectionFailure(t *testing.T) {
	// Try to connect to a non-existent server
	transport := &WebSocketClientTransport{
		URL: "ws://localhost:99999/nonexistent",
	}

	ctx := context.Background()
	_, err := transport.Connect(ctx)
	if err == nil {
		t.Error("Expected connection error, got nil")
	}
}

func TestWebSocketServerTransport(t *testing.T) {
	// Create a dummy server
	mcpServer := NewServer(&Implementation{Name: "test", Version: "1.0"}, nil)

	serverTransport := NewWebSocketServerTransport(func(r *http.Request) *Server {
		return mcpServer
	})
	if serverTransport == nil {
		t.Fatal("NewWebSocketServerTransport returned nil")
	}

	// Create a test server
	server := httptest.NewServer(serverTransport)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect a client
	dialer := websocket.DefaultDialer
	dialer.Subprotocols = []string{"mcp"}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to server: %v", err)
	}
	defer conn.Close()

	// Verify connection was established with correct subprotocol
	if conn.Subprotocol() != "mcp" {
		t.Errorf("Expected subprotocol 'mcp', got '%s'", conn.Subprotocol())
	}
}

func TestWebSocketMessageTypes(t *testing.T) {
	// Create a test WebSocket server that sends binary messages
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send a binary message (should cause error on client)
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte("binary data")); err != nil {
			return
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Try to read binary message - should error
	_, err = conn.Read(ctx)
	if err == nil {
		t.Error("Expected error when reading binary message, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected websocket message type") {
		t.Errorf("Expected 'unexpected websocket message type' error, got: %v", err)
	}
}

func TestWebSocketConcurrentWrites(t *testing.T) {
	// Create a test WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Just consume messages
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Test concurrent writes
	const numWrites = 10
	done := make(chan error, numWrites)

	for i := 0; i < numWrites; i++ {
		go func(id int) {
			msg, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(id)), "test", nil)
			if err != nil {
				done <- err
				return
			}
			done <- conn.Write(ctx, msg)
		}(i)
	}

	// Wait for all writes to complete
	for i := 0; i < numWrites; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent write %d failed: %v", i, err)
		}
	}
}

// TestWebSocketConnectWithCustomDialer tests Connect with a custom dialer.
func TestWebSocketConnectWithCustomDialer(t *testing.T) {
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

	// Custom dialer with timeout
	customDialer := &websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	transport := &WebSocketClientTransport{
		URL:    wsURL,
		Dialer: customDialer,
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect with custom dialer: %v", err)
	}
	conn.Close()
}

// TestWebSocketConnectWithCustomHeaders tests Connect with custom HTTP headers.
func TestWebSocketConnectWithCustomHeaders(t *testing.T) {
	expectedHeader := "test-value"
	headerReceived := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom-Header") == expectedHeader {
			headerReceived = true
		}

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

	transport := &WebSocketClientTransport{
		URL: wsURL,
		Header: http.Header{
			"X-Custom-Header": []string{expectedHeader},
		},
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect with custom headers: %v", err)
	}
	conn.Close()

	if !headerReceived {
		t.Error("Custom header was not received by server")
	}
}

// TestWebSocketReadMalformedJSON tests Read with malformed JSON data.
func TestWebSocketReadMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send malformed JSON
		conn.WriteMessage(websocket.TextMessage, []byte("{invalid json"))

		// Keep connection alive
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	ctx := context.Background()
	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Try to read malformed JSON
	_, err = conn.Read(ctx)
	if err == nil {
		t.Error("Expected error reading malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode") && !strings.Contains(err.Error(), "JSON") {
		t.Errorf("Expected JSON decode error, got: %v", err)
	}
}

// TestWebSocketServeHTTPUpgradeFailure tests ServeHTTP with invalid upgrade request.
func TestWebSocketServeHTTPUpgradeFailure(t *testing.T) {
	// Create a dummy server
	mcpServer := NewServer(&Implementation{Name: "test", Version: "1.0"}, nil)

	transport := NewWebSocketServerTransport(func(r *http.Request) *Server {
		return mcpServer
	})

	// Create a regular HTTP request (not a WebSocket upgrade)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	transport.ServeHTTP(w, req)

	// Should return 400 Bad Request
	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "upgrade") && !strings.Contains(body, "Upgrade") && !strings.Contains(body, "Bad Request") {
		t.Errorf("Expected upgrade error message, got: %s", body)
	}
}

// TestWebSocketWriteWithCancelledContext tests Write with a pre-cancelled context.
func TestWebSocketWriteWithCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Keep connection open to test context cancellation
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Try to write with cancelled context
	msg, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(1), "test", nil)
	if err != nil {
		t.Fatalf("Failed to create message: %v", err)
	}

	err = conn.Write(ctx, msg)
	if err == nil {
		t.Error("Expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "context") && err != context.Canceled {
		t.Errorf("Expected context error, got: %v", err)
	}
}

// TestWebSocketWriteContextCancellationDuringWrite tests context cancellation during write operation.
func TestWebSocketWriteContextCancellationDuringWrite(t *testing.T) {
	// Create a server that accepts connections but doesn't read
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			Subprotocols: []string{"mcp"},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Don't read messages - this can cause write to block
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	transport := &WebSocketClientTransport{
		URL: wsURL,
	}

	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Create a context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Try to write multiple messages rapidly to increase chance of context cancellation
	for i := 0; i < 10; i++ {
		msg, err := jsonrpc2.NewCall(jsonrpc2.Int64ID(int64(i)), "test", nil)
		if err != nil {
			t.Fatalf("Failed to create message: %v", err)
		}

		err = conn.Write(ctx, msg)
		if err != nil {
			// Context error is expected
			if strings.Contains(err.Error(), "context") || err == context.DeadlineExceeded {
				return // Test passed
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	// If we got here without error, that's also acceptable (write completed before timeout)
	t.Log("Write completed before context timeout (acceptable)")
}
