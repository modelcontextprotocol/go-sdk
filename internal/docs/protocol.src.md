# Support for the MCP Base protocol

%toc

## Lifecycle

## Transports

Transports should not be reused for multiple connections: if you need to create
multiple connections, use different transports.

### Streamable Transport

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

## Security

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
