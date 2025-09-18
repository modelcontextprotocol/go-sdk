# Support for MCP server features

%toc

## Prompts

**Server-side**:
MCP servers can provide LLM prompt templates (called simply _prompts_) to clients.
Every prompt has a required name which identifies it, and a set of named arguments, which are strings.
Construct a prompt with a name and descriptions of its arguments.
Associated with each prompt is a handler that expands the template given values for its arguments.
Use [`Server.AddPrompt`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#Server.AddPrompt)
to add a prompt along with its handler.
If `AddPrompt` is called before a server is connected, the server will have the `prompts` capability.
If all prompts are to be added after connection, set [`ServerOptions.HasPrompts`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.HasPrompts)
to advertise the capability.

**Client-side**:
To list the server's prompts, call
Call [`ClientSession.Prompts`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.Prompts) to get an iterator.
If needed, you can use the lower-level
[`ClientSession.ListPrompts`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.ListPrompts) to list the server's prompts.
Call [`ClientSession.GetPrompt`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.GetPrompt) to retrieve a prompt by name, providing
arguments for expansion.
Set [`ClientOptions.PromptListChangedHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions.PromptListChangedHandler) to be notified of changes in the list of prompts.

%include ../../mcp/server_example_test.go prompts -

## Resources

<!-- TODO -->

## Tools

<!-- TODO -->

## Utilities

### Completion

To support the
[completion](https://modelcontextprotocol.io/specification/2025-06-18/server/utilities/completion)
capability, the server needs a completion handler.

**Client-side**: completion is called using the
[`ClientSession.Complete`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.Complete)
method.

**Server-side**: completion is enabled by setting
[`ServerOptions.CompletionHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.CompletionHandler).
If this field is set to a non-nil value, the server will advertise the
`completions` server capability, and use this handler to respond to completion
requests.

%include ../../examples/server/completion/main.go completionhandler -

### Logging

MCP servers can send logging messages to MCP clients so their users can keep informed of progress.
(This form of logging is distinct from server-side logging, where the
server produces logs that remain server-side, for use by server maintainers.)

**Server-side**:
The minimum log level is part of the server state.
For stateful sessions, there is no default log level: no log messages will be sent
until the client calls `SetLevel` (see below).
For stateful sessions, the level defaults to "info".

[`ServerSession.Log`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerSession.Log) is the low-level way for servers to log to clients.
It sends a logging notification to the client if the level of the message
is at least the minimum log level.

For a simpler API, use [`NewLoggingHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#NewLoggingHandler) to obtain a [`slog.Handler`](https://pkg.go.dev/log/slog#Handler).
By setting [`LoggingHandlerOptions.MinInterval`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#LoggingHandlerOptions.MinInterval), the handler can be rate-limited
to avoid spamming clients with too many messages.

Servers always report the logging capability.


**Client-side**:

Set [`ClientOptions.LoggingMessageHandler`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientOptions.LoggingMessageHandler) to receive log messages.

Call [`ClientSession.SetLevel`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.SetLevel) to change the log level for a session.

%include ../../mcp/server_example_test.go logging -

### Pagination

Server-side feature lists may be
[paginated](https://modelcontextprotocol.io/specification/2025-06-18/server/utilities/pagination),
using cursors. The SDK supports this by default.

**Client-side**: The `ClientSession` provides methods returning
[iterators](https://go.dev/blog/range-functions) for each feature type.
These iterators are an `iter.Seq2[Feature, error]`, where the error value
indicates whether page retrieval failed.

- [`ClientSession.Prompts`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.Prompts)
  iterates prompts.
- [`ClientSession.Resource`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.Resource)
  iterates resources.
- [`ClientSession.ResourceTemplates`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.ResourceTemplates)
  iterates resource templates.
- [`ClientSession.Tools`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ClientSession.Tools)
  iterates tools.

The `ClientSession` also exposes `ListXXX` methods for fine-grained control
over pagination.

**Server-side**: pagination is on by default, so in general nothing is required
server-side. However, you may use
[`ServerOptions.PageSize`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#ServerOptions.PageSize)
to customize the page size.
