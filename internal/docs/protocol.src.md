# Support for the MCP Base protocol

%toc

## Lifecycle

## Transports

Transports should not be reused for multiple connections: if you need to create
multiple connections, use different transports.

### Stdio Transport

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
client, err := mcp.Connect(context.Background(), transport, &mcp.ClientOptions{...})
```

The `StreamableClientTransport` handles the HTTP requests and communicates with
the server using the streamable transport protocol.

#### Stateless Mode

#### Sessionless mode

### Custom transports

### Concurrency

In general, MCP offers no guarantees about concurrency semantics: if a client
or server sends a notification, the spec says nothing about when the peer
observes that notification relative to other request. However, the Go SDK
implements the following heuristics:

- If a notifying method (such as progress notification or
  `notifications/initialized`) returns, then it is guaranteed that the peer
  observes that notification before other notifications or calls.
- Calls (such as `tools/call`) are handled asynchronously with respect to
  eachother.

See
[modelcontextprotocol/go-sdk#26](https://github.com/modelcontextprotocol/go-sdk/issues/26)
for more background.

## Authorization

**TODO**

## Security

**TODO**

## Utilities

### Cancellation

Cancellation is implemented with context cancellation. Cancelling a context
used in a method on `ClientSession` or `ServerSession` will terminate the RPC
and send a "notifications/cancelled" message to the peer.

```go
ctx, cancel := context.WithCancel(context.Background())
go cs.CallTool(ctx, &CallToolParams{Name: "slow"})
cancel() // cancel the tool call
```

When an RPC exits due to a cancellation error, there's a guarantee that the
cancellation notification has been sent, but there's no guarantee that the
server has observed it (see [concurrency](#concurrency)).

### Ping

### Progress
