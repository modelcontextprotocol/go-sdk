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

	return newWebsocketConn(conn), nil
}

// NOTE: Connect sets the WebSocket subprotocol to "mcp" by overwriting any
// Dialer.Subprotocols value on a provided Dialer. If you need to negotiate
// different or additional subprotocols, perform dialing manually and wrap
// the resulting *websocket.Conn in a transport using newWebsocketConn.

// websocketConn implements the Connection interface for WebSocket connections.
type websocketConn struct {
	conn      *websocket.Conn
	sessionID string
	mu        sync.Mutex // Protects Write operations

	incoming  chan jsonrpc.Message
	err       error // terminal error
	closed    chan struct{}
	closeOnce sync.Once
}

func newWebsocketConn(conn *websocket.Conn) *websocketConn {
	c := &websocketConn{
		conn:      conn,
		sessionID: randText(),
		incoming:  make(chan jsonrpc.Message, 10),
		closed:    make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *websocketConn) readLoop() {
	defer close(c.closed)
	defer c.conn.Close()

	for {
		messageType, data, err := c.conn.ReadMessage()
		if err != nil {
			c.err = err
			return
		}

		if messageType != websocket.TextMessage {
			c.err = fmt.Errorf("unexpected websocket message type: %d (expected text)", messageType)
			return
		}

		msg, err := jsonrpc.DecodeMessage(data)
		if err != nil {
			// TODO: Log error? For now, we treat decode errors as fatal to the connection
			// or we could just skip bad messages.
			// The spec implies strict JSON-RPC.
			c.err = fmt.Errorf("failed to decode JSON-RPC message: %w", err)
			return
		}

		select {
		case c.incoming <- msg:
		case <-c.closed:
			return
		}
	}
}

// Read reads a JSON-RPC message from the WebSocket connection.
func (c *websocketConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-c.incoming:
		if !ok {
			if c.err != nil {
				// Check if it's a normal closure
				if websocket.IsCloseError(c.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return nil, io.EOF
				}
				return nil, c.err
			}
			return nil, io.EOF
		}
		return msg, nil
	case <-c.closed:
		if c.err != nil {
			if websocket.IsCloseError(c.err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil, io.EOF
			}
			return nil, c.err
		}
		return nil, io.EOF
	}
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
	case <-c.closed:
		return io.EOF
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

// Write is safe for concurrent use by multiple goroutines; writes are
// serialized using an internal mutex because gorilla/websocket requires
// only a single concurrent writer. If the provided ctx contains a deadline,
// the underlying socket write deadline will be set to that deadline.

// Close closes the WebSocket connection gracefully.
func (c *websocketConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		// Close the connection to unblock ReadMessage
		err = c.conn.Close()
	})
	// Wait for readLoop to finish
	<-c.closed
	return err
}

// SessionID returns the unique session identifier for this connection.
func (c *websocketConn) SessionID() string {
	return c.sessionID
}

// SessionID returns a small, random identifier associated with this
// connection. It's intended for logging/correlation and is not intended to be
// globally unique or secure.

// WebSocketServerTransport provides a WebSocket server transport for MCP servers.
// It can be used as an http.Handler to upgrade HTTP connections to WebSocket.
type WebSocketServerTransport struct {
	// Handler returns the Server instance to handle the connection.
	// If nil, ServeHTTP will return 500 Internal Server Error.
	Handler func(*http.Request) *Server

	// CheckOrigin returns true if the request Origin header is acceptable.
	// If nil, a safe default is used: return false if the Origin request header
	// is present and the origin host is not equal to request Host header.
	//
	// A function that always returns true can be used to allow all origins.
	CheckOrigin func(r *http.Request) bool

	upgrader websocket.Upgrader
}

// NewWebSocketServerTransport creates a new WebSocket server transport.
func NewWebSocketServerTransport(handler func(*http.Request) *Server) *WebSocketServerTransport {
	return &WebSocketServerTransport{
		Handler: handler,
	}
}

// ServeHTTP handles HTTP requests and upgrades them to WebSocket connections.
func (t *WebSocketServerTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if t.Handler == nil {
		http.Error(w, "server handler not configured", http.StatusInternalServerError)
		return
	}

	server := t.Handler(r)
	if server == nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	// Configure upgrader
	// Note: The transport enforces the 'mcp' subprotocol for the upgraded
	// connection; this will overwrite any Subprotocols previously set on the
	// embedded upgrader. If you need custom subprotocol negotiation, perform a
	// manual upgrade and wrap the connection; see examples/docs.
	t.upgrader.Subprotocols = []string{"mcp"}
	if t.CheckOrigin != nil {
		t.upgrader.CheckOrigin = t.CheckOrigin
	}

	conn, err := t.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade replies with an error if it fails
		return
	}

	// Create a wrapper transport for this single connection
	wrapper := &websocketServerConnTransport{conn: conn}

	// Connect the server to this transport
	// We use the request context for the connection
	session, err := server.Connect(r.Context(), wrapper, nil)
	if err != nil {
		// If connect fails, close the connection
		conn.Close()
		return
	}

	// Wait for the session to close
	// This blocks until the client disconnects or the server closes the session
	_ = session.Wait()
}

// websocketServerConnTransport is a temporary Transport implementation
// that returns an already established WebSocket connection.
type websocketServerConnTransport struct {
	conn *websocket.Conn
}

func (t *websocketServerConnTransport) Connect(ctx context.Context) (Connection, error) {
	return newWebsocketConn(t.conn), nil
}
