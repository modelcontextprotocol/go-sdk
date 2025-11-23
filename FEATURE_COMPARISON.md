# MCP SDK Feature Comparison

This document compares the Go SDK implementation with the TypeScript and Python SDKs to identify feature parity gaps.

Last Updated: November 23, 2025
Spec Version: 2025-03-26

## Legend

- ✅ Fully implemented
- ⚠️ Partially implemented
- ❌ Not implemented
- 🔄 Work in progress
- N/A Not applicable

## Transport Layer

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Stdio Transport** | ✅ | ✅ | ✅ | Standard subprocess communication |
| **SSE Transport** (deprecated 2024-11-05) | ✅ | ✅ (deprecated) | ✅ (deprecated) | HTTP + Server-Sent Events |
| **Streamable HTTP** (2025-03-26) | ✅ | ✅ | ✅ | Modern HTTP transport |
| - Session Management | ✅ | ✅ | ✅ | Session ID tracking |
| - Stateless Mode | ✅ | ✅ | ✅ | No session state |
| - JSON Responses | ✅ | ✅ | ✅ | Non-streaming mode |
| - SSE Streaming | ✅ | ✅ | ✅ | Server-Sent Events |
| - Resumability | ✅ | ✅ | ✅ | Event replay with Last-Event-ID |
| - DNS Rebinding Protection | ✅ | ✅ | ✅ | Security feature |
| **WebSocket Transport** | ✅ | ✅ | ❌ | Bidirectional real-time communication |
| **In-Memory Transport** | ✅ | ✅ | ✅ | For testing |
| **Custom Transports** | ✅ | ✅ | ✅ | Via Transport interface |

### Gap Analysis: WebSocket Transport

**Status:** ✅ Implemented

**Go Implementation:**
- `mcp/websocket.go` - WebSocketClientTransport and WebSocketServerTransport
- Uses `github.com/gorilla/websocket` library
- Bidirectional communication over standard WebSocket protocol
- Supports 'mcp' subprotocol for proper identification
- Thread-safe concurrent writes with mutex protection
- Example server at `examples/server/websocket/`
- Example client at `examples/client/websocket/`

**TypeScript Implementation:**
- `src/client/websocket.ts` - WebSocketClientTransport
- Uses standard WebSocket API
- Supports reconnection
- Bidirectional communication

## Core Protocol Features

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Initialization** | ✅ | ✅ | ✅ | initialize/initialized handshake |
| **Protocol Version Negotiation** | ✅ | ✅ | ✅ | Supports multiple versions |
| **Capabilities** | ✅ | ✅ | ✅ | Feature detection |
| **Shutdown** | ✅ | ✅ | ✅ | Graceful shutdown |
| **Cancellation** ($/cancelRequest) | ✅ | ✅ | ✅ | Request cancellation |
| **Progress Notifications** | ✅ | ✅ | ✅ | Progress tracking |
| **Ping/Pong** | ✅ | ✅ | ✅ | Keep-alive |
| **Error Handling** | ✅ | ✅ | ✅ | JSON-RPC errors |
| **Request/Response Correlation** | ✅ | ✅ | ✅ | Via request ID |

## Server Features - Tools

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **ListTools** | ✅ | ✅ | ✅ | Tool discovery |
| **CallTool** | ✅ | ✅ | ✅ | Tool invocation |
| **Tool Schemas** | ✅ | ✅ | ✅ | JSON Schema validation |
| **Type-Safe Tool Binding** | ✅ | ⚠️ | ⚠️ | Go uses generics, others use Zod/Pydantic |
| **Tool Progress** | ✅ | ✅ | ✅ | Progress during execution |
| **Structured Output** | ⚠️ | ✅ | ✅ | Need better ergonomics |
| **Tool Samples** | ⚠️ | ✅ | ✅ | Example invocations |

### Gap Analysis: Structured Output

**Status:** ⚠️ Basic support exists, needs improvement

**Current Go Implementation:**
```go
// Works but verbose
result := &mcp.CallToolResult{
    Content: []mcp.Content{
        &mcp.TextContent{Text: "result"},
    },
}
```

**TypeScript has:**
```typescript
return {
    content: [{ type: 'text', text: 'result' }],
    structuredContent: { key: 'value' } // Separate structured output
}
```

**Python (FastMCP) has:**
```python
@mcp.tool()
def greet(name: str) -> str:
    return f"Hello, {name}!"  # Automatically wrapped
```

**Recommendation:**
- Add helper functions for common content types
- Consider automatic wrapping for simple return types
- Add `StructuredContent` field to `CallToolResult` (already exists)
- Better documentation and examples

## Server Features - Resources

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **ListResources** | ✅ | ✅ | ✅ | Resource discovery |
| **ReadResource** | ✅ | ✅ | ✅ | Resource content access |
| **ResourceTemplates** | ✅ | ✅ | ✅ | URI templates |
| **Resource Subscriptions** | ✅ | ✅ | ✅ | Subscribe/unsubscribe |
| **ResourceUpdated Notifications** | ✅ | ✅ | ✅ | Change notifications |
| **Resource Pagination** | ✅ | ✅ | ✅ | Cursor-based |
| **Embedded Resources** | ✅ | ✅ | ✅ | Base64 encoded content |

## Server Features - Prompts

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **ListPrompts** | ✅ | ✅ | ✅ | Prompt discovery |
| **GetPrompt** | ✅ | ✅ | ✅ | Prompt retrieval |
| **Dynamic Arguments** | ✅ | ✅ | ✅ | Parameterized prompts |
| **Prompt Pagination** | ✅ | ✅ | ✅ | Cursor-based |

## Client Features

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Roots** | ✅ | ✅ | ✅ | File system roots |
| **RootsListChanged** | ✅ | ✅ | ✅ | Root change notifications |
| **Sampling** | ✅ | ✅ | ✅ | LLM sampling |
| - CreateMessage | ✅ | ✅ | ✅ | Message generation |
| - Tool Use | ✅ | ✅ | ✅ | Tool invocation in sampling |
| - Context Inclusion | ✅ | ✅ | ✅ | Include context in requests |
| **URL Elicitation** | ✅ | ✅ | ✅ | Secure input collection |

## Authentication & Security

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **OAuth 2.0 Client Flow** | ✅ | ✅ | ✅ | RFC 6749 |
| **OAuth 2.0 Token Verification** | ✅ | ⚠️ | ✅ | Go has full JWT support |
| **Resource Indicators** (RFC 8707) | ✅ | ✅ | ✅ | Resource-specific tokens |
| **Protected Resource Metadata** (RFC 9728) | ✅ | ✅ | ✅ | Metadata discovery |
| **Dynamic Client Registration** (RFC 7591) | ✅ | ✅ | ⚠️ | Go has full implementation |
| **Token Refresh** | ✅ | ✅ | ✅ | Automatic token refresh |
| **DNS Rebinding Protection** | ✅ | ✅ | ✅ | For HTTP transports |

### Gap Analysis: OAuth Implementation

**Status:** ✅ Go has most comprehensive OAuth support

**Go Advantages:**
- Full RFC 8707 Resource Indicators support
- Complete RFC 9728 Protected Resource Metadata
- Comprehensive RFC 7591 DCR implementation
- Built-in JWT verification

**TypeScript/Python:**
- More examples and documentation
- Simpler API for basic use cases

**Recommendation:**
- Add more OAuth examples
- Create quickstart guide for common OAuth flows
- Document integration with popular OAuth providers

## Utilities

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Completion** | ✅ | ✅ | ✅ | Autocomplete support |
| **Logging** | ✅ | ✅ | ✅ | Server logging to client |
| **Pagination** | ✅ | ✅ | ✅ | Cursor-based |

## Developer Experience

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Type Safety** | ✅ | ✅ | ✅ | All strongly typed |
| **Schema Generation** | ✅ | ✅ (Zod) | ✅ (Pydantic) | JSON Schema from types |
| **Middleware Support** | ✅ | ✅ | ✅ | Request/response interception |
| **Error Recovery** | ✅ | ✅ | ✅ | Graceful error handling |
| **Testing Utilities** | ✅ | ✅ | ✅ | In-memory transport, mocks |
| **CLI Tools** | ❌ | ✅ | ✅ | Inspector, dev tools |

### Gap Analysis: Developer Tools

**Status:** ❌ Missing CLI tools

**TypeScript Has:**
- `@modelcontextprotocol/inspector` - Interactive testing
- CLI for running servers

**Python Has:**
- `mcp` CLI tool
- `mcp dev` for development
- Server templates

**Recommendation:**
- Create `mcp` CLI tool in Go
- Add interactive testing tool
- Project scaffolding commands
- Server template generator

## Examples & Documentation

| Category | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Basic Server** | ✅ | ✅ | ✅ | Hello world example |
| **Basic Client** | ✅ | ✅ | ✅ | Simple client |
| **Tool Examples** | ✅ | ✅ | ✅ | Various tool patterns |
| **Resource Examples** | ✅ | ✅ | ✅ | Resource patterns |
| **Auth Examples** | ✅ | ✅ | ✅ | OAuth flows |
| **HTTP Server** | ✅ | ✅ | ✅ | Full HTTP server |
| **Middleware Examples** | ✅ | ✅ | ⚠️ | Request/response middleware |
| **Testing Examples** | ⚠️ | ✅ | ✅ | Need more test examples |
| **Migration Guide** | ❌ | N/A | N/A | From mark3labs/mcp-go |

### Gap Analysis: Examples

**Status:** ⚠️ Good coverage, but missing some patterns

**Missing Examples:**
1. Complex resource hierarchies
2. Real-world OAuth integration (with popular providers)
3. Performance tuning patterns
4. Error recovery strategies
5. Production deployment examples

**Recommendation:**
- Add `examples/production/` directory
- Create real-world use cases (file system, database, API)
- Add performance best practices examples
- Create troubleshooting examples

## High-Level Framework Support

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Low-Level API** | ✅ | ✅ | ✅ | Full control |
| **High-Level API** | ⚠️ | ⚠️ | ✅ | Python has FastMCP |
| **Decorators/Attributes** | N/A | ⚠️ | ✅ | Python @mcp.tool() |
| **Builder Pattern** | ⚠️ | ⚠️ | ✅ | FastMCP simplifies setup |

### Gap Analysis: High-Level API

**Status:** ⚠️ Go has good ergonomics but could improve

**Python FastMCP:**
```python
from mcp.server.fastmcp import FastMCP

mcp = FastMCP("My Server")

@mcp.tool()
def greet(name: str) -> str:
    return f"Hello, {name}!"

mcp.run()  # Handles transport automatically
```

**Go Current:**
```go
server := mcp.NewServer(&mcp.Implementation{
    Name: "My Server",
    Version: "1.0.0",
}, nil)

mcp.AddTool(server, &mcp.Tool{
    Name: "greet",
    Description: "Greet someone",
}, greetHandler)

server.Run(ctx, &mcp.StdioTransport{})
```

**Recommendation:**
- Current API is already quite good
- Could add optional builder pattern for common cases
- Not critical - Go's explicitness is a feature

## Performance & Scalability

| Feature | Go | TypeScript | Python | Notes |
|---------|----|-----------| -------|-------|
| **Concurrent Requests** | ✅ | ✅ | ✅ | All support concurrency |
| **Streaming Responses** | ✅ | ✅ | ✅ | SSE streaming |
| **Connection Pooling** | ✅ | ✅ | ✅ | HTTP connection reuse |
| **Memory Efficiency** | ✅ | ⚠️ | ⚠️ | Go's strength |
| **Benchmarks** | ⚠️ | ❌ | ❌ | Need more benchmarks |

## Priority Gaps Summary

### Critical (Must Fix)
1. **None** - Go SDK is feature-complete for spec compliance

### High Priority (Should Fix)
1. ✅ **Structured Output Helpers** - Improve ergonomics
2. ✅ **More Examples** - Real-world patterns
3. ✅ **CLI Tool** - Developer tooling
4. ✅ **Migration Guide** - From mark3labs/mcp-go

### Medium Priority (Nice to Have)
1. ✅ **WebSocket Transport** - Additional transport option
2. ✅ **Better Test Examples** - Testing patterns
3. ✅ **Production Examples** - Deployment patterns
4. ✅ **Performance Benchmarks** - Comprehensive benchmarking

### Low Priority (Future)
1. High-level builder API (current API is already good)
2. Visual debugging tools (can use TypeScript Inspector)

## Conclusion

The Go MCP SDK is **feature-complete** with respect to the MCP specification (2025-03-26). The main gaps are in developer experience and tooling:

1. **Core Protocol**: ✅ Full parity
2. **Transports**: ⚠️ Missing WebSocket (not in spec)
3. **Server Features**: ✅ Full parity
4. **Client Features**: ✅ Full parity
5. **Auth/Security**: ✅ Most comprehensive implementation
6. **Developer Tools**: ⚠️ Missing CLI tools
7. **Examples**: ⚠️ Good but could be better

The SDK is production-ready and can be used to build fully-featured MCP servers and clients. The recommended improvements are primarily about developer experience rather than core functionality.
