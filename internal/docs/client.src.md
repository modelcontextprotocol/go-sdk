# Support for MCP client features

%toc

## Roots

MCP allows clients to specify a set of filesystem
["roots"](https://modelcontextprotocol.io/specification/2025-06-18/client/roots).
The SDK supports this as follows:

**Client-side**: The SDK client always has the `roots.listChanged` capability.
To add roots to a client, use the
[`Client.AddRoots`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Client.AddRoots)
and
[`Client.RemoveRoots`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Client.RemoveRoots)
methods. If any servers are already [connected](protocol.md#lifecycle) to the
client, a call to `AddRoot` or `RemoveRoots` will result in a
`notifications/roots/list_changed` notification to each connected server.

**Server-side**: To query roots from the server, use the
[`ServerSession.ListRoots`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession.ListRoots)
method. To receive notifications about root changes, set
[`ServerOptions.RootsListChangedHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.RootsListChangedHandler).

%include ../../mcp/client_example_test.go roots -

## Sampling

[Sampling](https://modelcontextprotocol.io/specification/2025-06-18/client/sampling)
is a way for servers to leverage the client's AI capabilities. It is
implemented in the SDK as follows:

**Client-side**: To add the `sampling` capability to a client, set 
[`ClientOptions.CreateMessageHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions.CreateMessageHandler).
This function is invoked whenever the server requests sampling.

**Server-side**: To use sampling from the server, call
[`ServerSession.CreateMessage`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession.CreateMessage).

%include ../../mcp/client_example_test.go sampling -

## Elicitation

[Elicitation](https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation)
allows servers to request user inputs. It is implemented in the SDK as follows:

**Client-side**: To add the `elicitation` capability to a client, set
[`ClientOptions.ElicitationHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions.ElicitationHandler).
The elicitation handler must return a result that matches the requested schema;
otherwise, elicitation returns an error.

**Server-side**: To use elicitation from the server, call
[`ServerSession.Elicit`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession.Elicit).

%include ../../mcp/client_example_test.go elicitation -
