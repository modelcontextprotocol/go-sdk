# Go MCP SDK Onboarding Guide

Welcome to the Model Context Protocol (MCP) Go SDK! This guide will help you become a productive contributor to this project.

## 📚 Table of Contents

- [Understanding MCP](#understanding-mcp)
- [Project Structure](#project-structure)
- [Development Environment](#development-environment)
- [Architecture Overview](#architecture-overview)
- [Feature Completeness Status](#feature-completeness-status)
- [Contributing Workflow](#contributing-workflow)
- [Testing](#testing)
- [Common Tasks](#common-tasks)
- [Resources](#resources)

## 🎯 Understanding MCP

The Model Context Protocol (MCP) is a standard protocol for communication between AI models and external tools/resources. Think of it like HTTP for AI interactions.

**Key Concepts:**
- **Servers** expose tools, resources, and prompts that AI models can use
- **Clients** consume these capabilities and interact with servers
- **Transports** handle the bidirectional communication (stdio, HTTP+SSE, Streamable HTTP)
- **Protocol** defines JSON-RPC 2.0 based message exchange

## 📁 Project Structure

```
go-sdk/
├── mcp/                    # Core MCP implementation
│   ├── client.go          # Client implementation
│   ├── server.go          # Server implementation
│   ├── transport.go       # Transport interfaces
│   ├── streamable.go      # Streamable HTTP transport
│   ├── sse.go            # SSE (Server-Sent Events) transport
│   ├── cmd.go            # Command/subprocess transport
│   ├── protocol.go       # Core protocol types and logic
│   ├── tool.go           # Tool definitions and handling
│   ├── resource.go       # Resource handling
│   ├── prompt.go         # Prompt handling
│   └── *_test.go         # Comprehensive test suite
│
├── auth/                  # OAuth 2.0 authentication
│   ├── auth.go           # Token verification
│   └── client.go         # OAuth client flow
│
├── oauthex/              # OAuth extensions (RFC 8707, RFC 9728)
│   ├── oauth2.go         # OAuth 2.0 helpers
│   ├── dcr.go            # Dynamic Client Registration (RFC 7591)
│   └── resource_meta.go  # Protected Resource Metadata (RFC 9728)
│
├── jsonrpc/              # JSON-RPC public types
│   └── jsonrpc.go       
│
├── internal/             # Internal packages
│   ├── jsonrpc2/        # JSON-RPC 2.0 implementation (forked from x/tools)
│   ├── util/            # Utilities
│   └── xcontext/        # Context helpers
│
├── examples/             # Example implementations
│   ├── client/          # Client examples
│   ├── server/          # Server examples (basic, auth, tools, resources, etc.)
│   └── http/            # HTTP middleware examples
│
├── docs/                # Feature documentation
│   ├── README.md        # Feature index
│   ├── protocol.md      # Base protocol details
│   ├── client.md        # Client features
│   └── server.md        # Server features
│
└── design/              # Design documents
    └── design.md        # Architectural decisions and rationale
```

## 🛠 Development Environment

### Prerequisites

- **Go 1.23+** (specified in go.mod)
- Git
- A code editor with Go support (VS Code + Go extension recommended)

### Setup

```bash
# Clone the repository
git clone https://github.com/modelcontextprotocol/go-sdk.git
cd go-sdk

# Run tests to verify setup
go test ./...

# Install dependencies
go mod download

# Optional: Create a workspace for testing with other projects
# go work init ./go-sdk ./your-project
```

### Running Examples

```bash
# Basic server example (stdio transport)
cd examples/server/basic
go run main.go

# Test with a client in another terminal
cd examples/client/listfeatures
go run main.go -- <path-to-basic-server-binary>

# HTTP server example
cd examples/http
go run main.go
# Visit http://localhost:8080
```

## 🏗 Architecture Overview

### Core Design Principles

The Go SDK follows these key principles (from [design.md](design/design.md)):

1. **Complete**: Implements all MCP spec features
2. **Idiomatic**: Uses Go language features naturally
3. **Robust**: Well-tested and reliable
4. **Future-proof**: Designed for spec evolution
5. **Extensible**: Minimal but allows customization

### Key Architectural Decisions

#### Single Package Design
Unlike other SDKs, most APIs live in the `mcp` package (similar to `net/http`). This:
- Improves discoverability
- Avoids arbitrary package boundaries
- Simplifies imports

#### Transport Abstraction
```go
type Transport interface {
    Connect(ctx context.Context) (Connection, error)
}

type Connection interface {
    Read(context.Context) (jsonrpc.Message, error)
    Write(context.Context, jsonrpc.Message) error
    Close() error
}
```

This low-level interface is easier to implement for custom transports.

#### Type-Safe Tool Binding
Uses Go generics for type-safe tool handlers:

```go
func AddTool[In, Out any](
    server *Server, 
    tool *Tool, 
    handler func(context.Context, *CallToolRequest, In) (*CallToolResult, Out, error)
)
```

#### JSON-RPC Foundation
Uses a forked version of `golang.org/x/tools/internal/jsonrpc2` with proper support for:
- Request/response correlation
- Cancellation propagation
- Concurrent message handling

## 📊 Feature Completeness Status

### ✅ Implemented Features

#### Base Protocol
- [x] Lifecycle (initialize, initialized, shutdown)
- [x] JSON-RPC 2.0 message handling
- [x] Request/response correlation
- [x] Cancellation ($/cancelRequest)
- [x] Progress notifications
- [x] Ping

#### Transports
- [x] **Stdio transport** - Communication via stdin/stdout
- [x] **Command transport** - Launch subprocess and communicate
- [x] **In-memory transport** - For testing
- [x] **SSE transport** (2024-11-05 spec) - HTTP with Server-Sent Events (deprecated)
- [x] **Streamable HTTP transport** (2025-03-26 spec) - Modern HTTP transport
  - [x] Session management
  - [x] Stateless mode
  - [x] JSON responses (non-streaming)
  - [x] SSE responses (streaming)
  - [x] Resumability (event replay)
  - [x] DNS rebinding protection

#### Server Features
- [x] Tools
  - [x] ListTools
  - [x] CallTool
  - [x] Tool schemas (via JSON schema)
  - [x] Type-safe tool binding
- [x] Resources
  - [x] ListResources
  - [x] ReadResource
  - [x] ResourceTemplates
  - [x] ResourceUpdated notifications
  - [x] Subscribe/unsubscribe
- [x] Prompts
  - [x] ListPrompts
  - [x] GetPrompt
  - [x] Dynamic arguments
- [x] Completion
- [x] Logging
- [x] Pagination (cursor-based)

#### Client Features
- [x] Roots (file system roots)
- [x] Sampling (LLM interactions)
- [x] URL elicitation (secure input collection)

#### Security & Auth
- [x] OAuth 2.0 client flow (RFC 6749)
- [x] OAuth 2.0 token verification
- [x] Resource Indicators (RFC 8707)
- [x] Protected Resource Metadata (RFC 9728)
- [x] Dynamic Client Registration (RFC 7591)
- [x] DNS rebinding protection

### ⚠️ Missing Features (Gaps vs Other SDKs)

#### Critical Gaps
1. **WebSocket Transport** - TypeScript SDK has this
   - Priority: Medium (not in spec, but useful)
   - Location: Would go in `mcp/websocket.go`

2. **Structured Output/Structured Content** - Python SDK has enhanced support
   - Priority: High (quality of life)
   - Current: Basic support exists, could be improved
   - Enhancement: Better type-safe output schemas

3. **Tool Progress Notifications** - Better ergonomics needed
   - Priority: Medium
   - Current: Functional but verbose
   - Enhancement: Simpler API for progress tracking

#### Documentation Gaps
1. Migration guide from mark3labs/mcp-go (mentioned in design.md)
2. More real-world examples for:
   - OAuth flows
   - Complex resource patterns
   - Error handling patterns
3. Performance tuning guide

#### Testing Gaps
1. Integration tests with real HTTP servers
2. Conformance tests against spec examples
3. Performance benchmarks for all transports
4. Fuzz testing for protocol parsing

## 🤝 Contributing Workflow

### Finding Work

1. **Check Issues**: Look for issues labeled:
   - [`help wanted`](https://github.com/modelcontextprotocol/go-sdk/labels/help%20wanted)
   - [`good first issue`](https://github.com/modelcontextprotocol/go-sdk/labels/good%20first%20issue)
   
2. **Comment First**: Before starting work, comment on the issue to claim it

3. **For Unlabeled Issues**: Ask maintainers before starting to avoid duplicated effort

### Development Process

1. **Create a Branch**
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Write Code**
   - Follow [Google Go Style Guide](https://google.github.io/styleguide/go/)
   - Add tests for new functionality
   - Update documentation

3. **Test Thoroughly**
   ```bash
   # Run all tests
   go test ./...
   
   # Run with race detector
   go test -race ./...
   
   # Run specific package tests
   go test ./mcp -v
   ```

4. **Commit Messages**
   Follow [Go commit message style](https://go.dev/wiki/CommitMessage):
   ```
   mcp: add WebSocket transport support
   
   This implements a WebSocket transport that conforms to the
   Transport interface. The implementation uses gorilla/websocket
   and supports both client and server modes.
   
   Fixes #123
   ```

5. **Create Pull Request**
   - Reference related issues
   - Describe what changed and why
   - Include example usage if adding new features

### Code Review

- PRs require approval from maintainers
- Be responsive to feedback
- Tests must pass (CI runs automatically)

## 🧪 Testing

### Test Organization

```bash
# Unit tests for core functionality
go test ./mcp

# Integration tests (may require setup)
go test ./mcp -tags=integration

# Specific test
go test ./mcp -run TestClientServerBasic

# Benchmarks
go test ./mcp -bench=.

# Coverage
go test ./mcp -cover
go test ./mcp -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Writing Tests

Example test structure:
```go
func TestMyFeature(t *testing.T) {
    // Setup: Create in-memory transport pair
    ct, st := mcp.NewInMemoryTransports()
    
    // Create server
    server := mcp.NewServer(&mcp.Implementation{
        Name: "test-server",
        Version: "1.0.0",
    }, nil)
    
    // Add features to server
    mcp.AddTool(server, &mcp.Tool{
        Name: "test-tool",
        Description: "A test tool",
    }, handleTestTool)
    
    // Create client
    client := mcp.NewClient(&mcp.Implementation{
        Name: "test-client", 
        Version: "1.0.0",
    }, nil)
    
    // Connect
    ctx := context.Background()
    serverDone := make(chan error, 1)
    go func() {
        serverDone <- server.Run(ctx, st)
    }()
    
    _, err := client.Connect(ctx, ct, nil)
    if err != nil {
        t.Fatal(err)
    }
    defer client.Close()
    
    // Test functionality
    result, err := client.CallTool(ctx, &mcp.CallToolRequest{
        Name: "test-tool",
        Arguments: map[string]any{"input": "test"},
    })
    if err != nil {
        t.Fatal(err)
    }
    
    // Verify results
    // ...
}
```

## 📝 Common Tasks

### Adding a New Transport

1. Create `mcp/mytransport.go`
2. Implement `Transport` interface:
   ```go
   type MyTransport struct { /* config */ }
   
   func (t *MyTransport) Connect(ctx context.Context) (Connection, error) {
       // Return a Connection implementation
   }
   ```
3. Implement `Connection` interface (often as unexported type)
4. Add tests in `mcp/myransport_test.go`
5. Add example in `examples/server/myransport/`
6. Document in `docs/protocol.md`

### Adding a New Tool

Server side:
```go
type GreetInput struct {
    Name string `json:"name" jsonschema:"the person to greet"`
}

type GreetOutput struct {
    Message string `json:"message"`
}

func greetHandler(ctx context.Context, req *mcp.CallToolRequest, input GreetInput) (*mcp.CallToolResult, GreetOutput, error) {
    return nil, GreetOutput{
        Message: fmt.Sprintf("Hello, %s!", input.Name),
    }, nil
}

server := mcp.NewServer(/* ... */)
mcp.AddTool(server, &mcp.Tool{
    Name: "greet",
    Description: "Greets a person by name",
}, greetHandler)
```

### Adding Documentation

1. Feature docs go in `docs/` (auto-generated from `internal/docs/*.src.md`)
2. Edit source files in `internal/docs/`
3. Run `go generate ./...` to regenerate
4. Or edit directly if docs are hand-maintained

### Running Examples

```bash
# Most examples support --help
go run examples/server/basic/main.go --help

# HTTP examples typically need a browser
go run examples/http/main.go
# Open http://localhost:8080
```

## 📖 Resources

### Documentation
- [MCP Specification](https://modelcontextprotocol.io/specification/2025-03-26)
- [Go SDK Design Doc](design/design.md)
- [Feature Documentation](docs/README.md)
- [Contributing Guide](CONTRIBUTING.md)

### SDK Comparisons
- [TypeScript SDK](https://github.com/modelcontextprotocol/typescript-sdk)
- [Python SDK](https://github.com/modelcontextprotocol/python-sdk)

### Go Resources
- [Effective Go](https://go.dev/doc/effective_go)
- [Go Style Guide](https://google.github.io/styleguide/go/)
- [Go Wiki](https://go.dev/wiki/)

### Getting Help
- GitHub Issues: For bugs and feature requests
- GitHub Discussions: For design discussions
- Discord: [MCP Community Server](https://discord.gg/mcp) (if available)

## 🎯 Quick Start Projects

Here are some good first contributions:

### Beginner-Friendly
1. Add more examples to `examples/` directory
2. Improve error messages with better context
3. Add godoc examples to existing functions
4. Fix TODOs in the codebase (run `grep -r TODO .`)

### Intermediate
1. Implement WebSocket transport
2. Add structured output helper functions
3. Improve tool progress notification ergonomics
4. Add integration tests for Streamable HTTP

### Advanced
1. Performance optimizations (check benchmarks)
2. Implement conformance test suite
3. Add fuzz testing for protocol parsing
4. Write migration guide from mark3labs/mcp-go

## 🔍 Code Archaeology

### Finding Relevant Code

```bash
# Find where a feature is implemented
git grep -n "CallTool"

# Find tests for a feature
find . -name "*_test.go" -exec grep -l "TestCallTool" {} \;

# Find examples
find examples -name "*.go" -exec grep -l "CallTool" {} \;

# See history of a file
git log -p mcp/tool.go
```

### Understanding Design Decisions

1. Read `design/design.md` first
2. Check PR descriptions for context
3. Look for comments in code (especially with issue numbers)
4. Read test files - they show intended usage

---

## Welcome Aboard! 🚀

You're now ready to contribute to the Go MCP SDK. Start small, ask questions, and enjoy building tools that enhance AI capabilities!

**Next Steps:**
1. ✅ Run `go test ./...` to verify your setup
2. ✅ Pick an issue from the tracker
3. ✅ Join the community discussions
4. ✅ Make your first contribution!

Happy coding! 🎉
