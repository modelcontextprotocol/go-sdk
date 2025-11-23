<!--
  Minimal WebSocket usage guide for MCP transport.
  This file contains short examples and production guidance for the WebSocket
  client and server transports that are part of the MCP Go SDK.
-->

# WebSocket Transport (MCP)

This document explains how to use the `WebSocketClientTransport` and
`WebSocketServerTransport` present in the MCP SDK. The transport uses the
`mcp` WebSocket subprotocol for JSON-RPC-over-WebSocket communication.

## Key facts

- The default `Connect` enforces the `mcp` subprotocol by overwriting the
  Dialer's Subprotocols value.
- `WebSocketClientTransport.Header` can be used to pass custom headers such
  as `Authorization` during the handshake.
- `WebSocketClientTransport.Dialer` can be set to manage handshake timeouts
  or other dial settings, but `Subprotocols` will be overwritten.
- `websocketConn` reads messages in a separate goroutine and decodes JSON-RPC
  text messages. Decoding failures and non-text messages are treated as fatal
  and close the connection.
- `Write` is safe for concurrent use; it serializes writes due to underlying
  gorilla/websocket restrictions.

## Client Example

Use the built-in client transport when you want a simple client:

```go
dialer := &websocket.Dialer{HandshakeTimeout: 5 * time.Second}
transport := &mcp.WebSocketClientTransport{
    URL:    "wss://example.org/mcp",
    Dialer: dialer,
    Header: http.Header{"Authorization": {"Bearer TOKEN"}},
}
ctx := context.Background()
conn, err := transport.Connect(ctx)
if err != nil { /* handle error */ }
defer conn.Close()
// Use conn.Read/Write, or pass to client layer that handles message semantics.
```

## Server Example

Basic server usage with `NewWebSocketServerTransport`:

```go
server := mcp.NewServer(impl, nil)
wsHandler := mcp.NewWebSocketServerTransport(func(r *http.Request) *mcp.Server {
    // Optionally inspect auth headers and return server only for valid requests.
    return server
})
wsHandler.CheckOrigin = func(r *http.Request) bool { /* your policy */ return true }
http.Handle("/mcp", wsHandler)
log.Fatal(http.ListenAndServe("":8080, nil))
```

### Manual Upgrade (custom behavior)

If you need to control subprotocol negotiation or send graceful close frames,
perform a manual upgrade and then wrap `*websocket.Conn` manually using
`newWebsocketConn`:

```go
upgrader := websocket.Upgrader{CheckOrigin: customAllowed}
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil { http.Error(w, "upgrade failed", http.StatusBadRequest); return }
    // You can check conn.Subprotocol() here and send custom frames.
    c := mcp.NewWebSocketServerTransport(func(_ *http.Request) *mcp.Server {return server})
    // Or you can call server.Connect directly if you need an existing session.
    transportConn := mcp.NewWebsocketConn(conn)
    server.Connect(r.Context(), transportConn, nil)
})
```

## Production Notes

- Keep connections alive and reuse them rather than handshake per request.
- Configure `CheckOrigin` to a strict policy in browsers/production.
- Use TLS (`wss://`) for security and avoid sending plaintext tokens.
- Use `SessionID()` for correlation in logs if you need short-term traceability.

## Behavior to watch

- If your consumer cannot keep up with `incoming` buffered messages (default
  buffer size is 10), the `readLoop` may block until the channel is drained.
- Decode errors in `readLoop` will terminate the connection; adjust code if
  you need tolerant processing.
