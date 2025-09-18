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

<!-- TODO -->

### Completion

<!-- TODO -->

### Logging

<!-- TODO -->

### Pagination

<!-- TODO -->
