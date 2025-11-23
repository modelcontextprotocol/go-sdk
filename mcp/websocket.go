// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// WebSocketClientTransport provides a WebSocket-based transport for MCP clients.
// It connects to a WebSocket server and uses the 'mcp' subprotocol for communication.
type WebSocketClientTransport struct {
	// URL is the WebSocket server URL (e.g., "ws://localhost:8080/mcp" or "wss://example.com/mcp")
	URL string

	// Dialer is the WebSocket dialer to use. If nil, a default dialer will be used.
	Dialer *websocket.Dialer

	// Header specifies additional HTTP headers to send during the WebSocket handshake.
	Header http.Header
}

// Connect establishes a WebSocket connection to the configured URL.
func (t *WebSocketClientTransport) Connect(ctx context.Context) (Connection, error) {
	dialer := t.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}

	// Set the MCP subprotocol
	dialer.Subprotocols = []string{"mcp"}

	// Establish WebSocket connection
	conn, resp, err := dialer.DialContext(ctx, t.URL, t.Header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket connection failed: %w (status: %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("websocket connection failed: %w", err)
	}

	return &websocketConn{
		conn:      conn,
		sessionID: randText(),
	}, nil
}

// websocketConn implements the Connection interface for WebSocket connections.
type websocketConn struct {
	conn      *websocket.Conn
	sessionID string
	mu        sync.Mutex // Protects Write operations
	closeOnce sync.Once
}

// Read reads a JSON-RPC message from the WebSocket connection.
func (c *websocketConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	// Set up context cancellation
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			c.conn.Close()
		case <-done:
		}
	}()

	// Read message from WebSocket
	messageType, data, err := c.conn.ReadMessage()
	if err != nil {
		if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("websocket read error: %w", err)
	}

	// Ensure we received a text message (JSON-RPC should be text)
	if messageType != websocket.TextMessage {
		return nil, fmt.Errorf("unexpected websocket message type: %d (expected text)", messageType)
	}

	// Decode the JSON-RPC message
	msg, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JSON-RPC message: %w", err)
	}

	return msg, nil
}

// Write sends a JSON-RPC message over the WebSocket connection.
func (c *websocketConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	// Encode the message before acquiring lock to reduce contention
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to encode JSON-RPC message: %w", err)
	}

	// Check context before expensive operations
	if ctx.Err() != nil {
		return ctx.Err()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Fast path: if context is already done, bail out immediately
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Set write deadline if context has deadline
	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetWriteDeadline(deadline)
		defer c.conn.SetWriteDeadline(time.Time{}) // Reset deadline
	}

	// Write directly - gorilla/websocket handles blocking
	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("websocket write error: %w", err)
	}

	return nil
}

// Close closes the WebSocket connection gracefully.
func (c *websocketConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		// Close the connection directly
		// The gorilla/websocket library handles the close handshake
		err = c.conn.Close()
	})
	return err
}

// SessionID returns the unique session identifier for this connection.
func (c *websocketConn) SessionID() string {
	return c.sessionID
}

// WebSocketServerTransport provides a WebSocket server transport for MCP servers.
// It can be used as an http.Handler to upgrade HTTP connections to WebSocket.
type WebSocketServerTransport struct {
	upgrader websocket.Upgrader
}

// NewWebSocketServerTransport creates a new WebSocket server transport.
func NewWebSocketServerTransport() *WebSocketServerTransport {
	return &WebSocketServerTransport{
		upgrader: websocket.Upgrader{
			Subprotocols: []string{"mcp"},
			CheckOrigin: func(r *http.Request) bool {
				// By default, allow all origins. In production, implement proper origin checking.
				return true
			},
		},
	}
}

// ServeHTTP handles HTTP requests and upgrades them to WebSocket connections.
func (t *WebSocketServerTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := t.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("WebSocket upgrade failed: %v", err), http.StatusBadRequest)
		return
	}

	// Connection is now established; the server should handle it
	// This is typically done by wrapping this in a Server and calling Serve()
	_ = conn
}

// Accept accepts a new WebSocket connection. This is used internally by the server.
func (t *WebSocketServerTransport) Accept(conn *websocket.Conn) Connection {
	return &websocketConn{
		conn:      conn,
		sessionID: randText(),
	}
}
