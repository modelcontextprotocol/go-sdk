# MCP WebSocket Examples

This directory contains two example servers showing different approaches to
serving MCP over WebSocket:

- `main.go` demonstrates a manual upgrade: perform the `websocket.Upgrader`,
  control subprotocols and control frames explicitly, and wrap the connection
  in an implementation that satisfies `mcp.Transport`/`mcp.Connection`.
- `new_transport_main.go` demonstrates using the library-provided
  `NewWebSocketServerTransport` which returns an `http.Handler` that upgrades
  connections and delegates to an `mcp.Server`.

When to use which example:

- Manual Upgrade (`main.go`): Use when you need fine-grained control over the
  WebSocket handshake (custom subprotocol negotiation, custom close frames,
  more direct socket control, or special logging hooks).
- Library-Provided Handler (`new_transport_main.go`): Use when you want a
  minimal, supported approach: the transport handles the upgrade, and you
  return a `*mcp.Server` instance for that request. This is the simplest
  path for standard MCP deployments.

Both examples are valid and demonstrate patterns that may be useful in
production. See `docs/websocket.md` for usage guidance and recommended
production configurations (TLS, CheckOrigin, connection reuse).
