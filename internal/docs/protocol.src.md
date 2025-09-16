# Support for the MCP base protocol

%toc

## Lifecycle

The SDK provides an API for defining both MCP clients and servers, and
connecting them over various transports. When a client and server are
connected, it creates a logical session, which follows the MCP spec's
[lifecycle](https://modelcontextprotocol.io/specification/2025-06-18/basic/lifecycle).

In this SDK, both a
[`Client`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Client)
and
[`Server`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Server)
can handle multiple peers. Every time a new peer is connected, it creates a new
session.

- A `Client` is a logical MCP client, configured with various
  [`ClientOptions`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions).
- When a client is connected to a server using
  [`Client.Connect`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Client.Connect),
  it creates a
  [`ClientSession`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession).
  This session is initialized during the `Connect` method, and provides methods
  to communicate with the server peer.
- A `Server` is a logical MCP server, configure with various
  [`ServerOptions`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions).
- When a server is connected to a client using
  [`Server.Connect`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Server.Connect),
  it creates a
  [`ServerSession`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession).
  This session is not initialized until the client sends the
  `notifications/initialized` message. Use `ServerOptions.InitializedHandler`
  to listen for this event, or just use the session through various feature
  handlers (such as a `ToolHandler`). Requests to the server are rejected until
  the client has initialized the session.

Both `ClientSession` and `ServerSession` have a `Close` method to terminate the
session, and a `Wait` method to await session termination by the peer. Typically,
it is the client's responsibility to end the session.

%include ../../mcp/mcp_example_test.go lifecycle -

## Transports

A
[transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)
can be used to send JSON-RPC messages from client to server, or vice-versa.

In the SDK, this is achieved by implementing the
[`Transport`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Transport)
interface, which creates a (logical) bidirectional stream of JSON-RPC messages.
Most transport implementations described below are specific to either the
client or server: a "client transport" is something that can be used to connect
a client to a server, and a "server transport" is something that can be used to
connect a server to a client. However, it's possible for a transport to be both
a client and server transport, such as the `InMemoryTransport` used in the
lifecycle example above.

Transports should not be reused for multiple connections: if you need to create
multiple connections, use different transports.

### Stdio Transport

In the
[`stdio`](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#stdio)
transport clients communicate with an MCP server running in a subprocess using
newline-delimited JSON over its stdin/stdout.

**Client-side**: the client side of the `stdio` transport is implemented by
[`CommandTransport`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#CommandTransport),
which starts the a given `exec.Cmd` as a subprocess and communicates over its
stdin/stdout.

**Server-side**: the server side of the `stdio` transport is implemented by
`StdioTransport`, which connects over the current processes `os.Stdin` and
`os.Stdout`.

### Streamable Transport

The [streamable
transport](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#streamable-http)
API is implemented across three types:

- `StreamableHTTPHandler`: an`http.Handler` that serves streamable MCP
  sessions.
- `StreamableServerTransport`: a `Transport` that implements the server side of
  the streamable transport.
- `StreamableClientTransport`: a `Transport` that implements the client side of
  the streamable transport.

To create a streamable MCP server, you create a `StreamableHTTPHandler` and
pass it an `mcp.Server`:

%include ../../mcp/streamable_example_test.go streamablehandler -

The `StreamableHTTPHandler` handles the HTTP requests and creates a new
`StreamableServerTransport` for each new session. The transport is then used to
communicate with the client.

On the client side, you create a `StreamableClientTransport` and use it to
connect to the server:

```go
transport := &mcp.StreamableClientTransport{
	Endpoint: "http://localhost:8080/mcp",
}
client, err := mcp.Connect(ctx, transport, &mcp.ClientOptions{...})
```

The `StreamableClientTransport` handles the HTTP requests and communicates with
the server using the streamable transport protocol.

#### Stateless Mode

<!-- TODO -->

#### Sessionless mode

<!-- TODO -->

### Custom transports

<!-- TODO -->

### Concurrency

In general, MCP offers no guarantees about concurrency semantics: if a client
or server sends a notification, the spec says nothing about when the peer
observes that notification relative to other request. However, the Go SDK
implements the following heuristics:

- If a notifying method (such as `notifications/progress` or
  `notifications/initialized`) returns, then it is guaranteed that the peer
  observes that notification before other notifications or calls from the same
  client goroutine.
- Calls (such as `tools/call`) are handled asynchronously with respect to
  each other.

See
[modelcontextprotocol/go-sdk#26](https://github.com/modelcontextprotocol/go-sdk/issues/26)
for more background.

## Authorization

<!-- TODO -->

## Security

<!-- TODO -->

## Utilities

### Cancellation

Cancellation is implemented with context cancellation. Cancelling a context
used in a method on `ClientSession` or `ServerSession` will terminate the RPC
and send a "notifications/cancelled" message to the peer.

When an RPC exits due to a cancellation error, there's a guarantee that the
cancellation notification has been sent, but there's no guarantee that the
server has observed it (see [concurrency](#concurrency)).

%include ../../mcp/mcp_example_test.go cancellation -

### Ping

[Ping](https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/ping)
support is symmetrical for client and server.

To initiate a ping, call
[`ClientSession.Ping`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.Ping)
or
[`ServerSession.Ping`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession.Ping).

To have the client or server session automatically ping its peer, and close the
session if the ping fails, set
[`ClientOptions.KeepAlive`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions.KeepAlive)
or
[`ServerOptions.KeepAlive`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.KeepAlive).

### Progress

[Progress](https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/progress)
reporting is possible by reading the progress token from request metadata and
calling either
[`ClientSession.NotifyProgress`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.NotifyProgress)
or
[`ServerSession.NotifyProgress`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession.NotifyProgress).
To listen to progress notifications, set
[`ClientOptions.ProgressNotificationHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions.ProgressNotificationHandler)
or
[`ServerOptions.ProgressNotificationHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.ProgressNotificationHandler).

Issue #460 discusses some potential ergonomic improvements to this API.

%include ../../mcp/mcp_example_test.go progress -
