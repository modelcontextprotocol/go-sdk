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
- A `Server` is a logical MCP server, configured with various
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
which starts the a `exec.Cmd` as a subprocess and communicates over its
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

The streamable server supports a _stateless mode_ by setting
[`StreamableHTTPOptions.Stateless`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#StreamableHTTPOptions.Stateless),
which is where the server does not perform any validation of the session id,
and uses a temporary session to handle requests. In this mode, it is impossible
for the server to make client requests, as there is no way for the client's
response to reach the session.

However, it is still possible for the server to access the `ServerSession.ID`
to see the logical session

> [!WARNING]
> Stateless mode is not directly discussed in the spec, and is still being
> defined. See modelcontextprotocol/modelcontextprotocol#1364,
> modelcontextprotocol/modelcontextprotocol#1372, or
> modelcontextprotocol/modelcontextprotocol#11442 for potential refinements.

_See [examples/server/distributed](../examples/server/distributed/main.go) for
an example using statless mode to implement a server distributed across
multiple processes._

### Custom transports

The SDK supports [custom
transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#custom-transports)
by implementing the
[`Transport`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Transport)
interface: a logical bidirectional stream of JSON-RPC messages.

_Full example: [examples/server/custom-transport](../examples/server/custom-transport/main.go)._

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

### Server

To write an MCP server that performs authorization,
use [`RequireBearerToken`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#RequireBearerToken).
This function is middleware that wraps an HTTP handler, such as the one returned
by [`NewStreamableHTTPHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#NewStreamableHTTPHandler), to provide support for verifying bearer tokens.
The middleware function checks every request for an Authorization header with a bearer token,
and invokes the 
[`TokenVerifier`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#TokenVerifier)
 passed to `RequireBearerToken` to parse the token and perform validation.
The middleware function checks expiration and scopes (if they are provided in
[`RequireBearerTokenOptions.Scopes`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#RequireBearerTokenOptions.Scopes)), so the
`TokenVerifer` doesn't have to.
If [`RequireBearerTokenOptions.ResourceMetadataURL`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#RequireBearerTokenOptions.ResourceMetadataURL) is set and verification fails, 
the middleware function sets the WWW-Authenticate header as required by the [Protected Resource
Metadata spec](https://datatracker.ietf.org/doc/html/rfc9728).

Server handlers, such as tool handlers, can obtain the `TokenInfo` returned by the `TokenVerifier`
from `req.Extra.TokenInfo`, where `req` is the handler's request. (For example, a
[`CallToolRequest`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#CallToolRequest).)
HTTP handlers wrapped by the `RequireBearerToken` middleware can obtain the `TokenInfo` from the context
with [`auth.TokenInfoFromContext`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#TokenInfoFromContext).
 

The  [_auth middleware example_](https://github.com/modelcontextprotocol/go-sdk/tree/main/examples/server/auth-middleware) shows how to implement authorization for both JWT tokens and API keys.

### Client

Client-side OAuth is implemented by setting  
[`StreamableClientTransport.HTTPClient`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v0.5.0/mcp#StreamableClientTransport.HTTPClient) to a custom [`http.Client`](https://pkg.go.dev/net/http#Client)
Additional support is forthcoming; see #493.

## Security

Here we discuss the mitigations described under
the MCP spec's [Security Best Practices](https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices) section, and how we handle them.

### Confused Deputy

The [mitigation](https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices#mitigation), obtaining user consent for dynamically registered clients,
happens on the MCP client. At present we don't provide client-side OAuth support.


### Token Passthrough

The [mitigation](https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices#mitigation-2), accepting only tokens that were issued for the server, depends on the structure
of tokens and is the responsibility of the
[`TokenVerifier`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#TokenVerifier)
provided to 
[`RequireBearerToken`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#RequireBearerToken).

### Session Hijacking

The [mitigations](https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices#mitigation-3) are as follows:

- _Verify all inbound requests_. The [`RequireBearerToken`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/auth#RequireBearerToken)
middleware function will verify all HTTP requests that it receives. It is the
user's responsibility to wrap that function around all handlers in their server.

- _Secure session IDs_. This SDK generates cryptographically secure session IDs by default.
If you create your own with 
[`ServerOptions.GetSessionID`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.GetSessionID), it is your responsibility to ensure they are secure.
If you are using Go 1.24 or above,
we recommend using [`crypto/rand.Text`](https://pkg.go.dev/crypto/rand#Text) 

- _Binding session IDs to user information_. This is an application requirement, out of scope
for the SDK. You can create your own session IDs by setting
[`ServerOptions.GetSessionID`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.GetSessionID).

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
