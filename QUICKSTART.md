# Go MCP SDK Quick Reference

Quick reference for common tasks when building with the Go MCP SDK.

## Table of Contents

- [Project Setup](#project-setup)
- [Creating Servers](#creating-servers)
- [Creating Clients](#creating-clients)
- [Transports](#transports)
- [Tools](#tools)
- [Resources](#resources)
- [Prompts](#prompts)
- [Authentication](#authentication)
- [Testing](#testing)
- [Common Patterns](#common-patterns)

## Project Setup

### Initialize a New Project

```bash
mkdir my-mcp-server
cd my-mcp-server
go mod init github.com/yourname/my-mcp-server
go get github.com/modelcontextprotocol/go-sdk/mcp
```

### Minimal Server

```go
package main

import (
    "context"
    "log"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
    server := mcp.NewServer(&mcp.Implementation{
        Name:    "my-server",
        Version: "1.0.0",
    }, nil)
    
    if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
        log.Fatal(err)
    }
}
```

### Minimal Client

```go
package main

import (
    "context"
    "log"
    "os/exec"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
    client := mcp.NewClient(&mcp.Implementation{
        Name:    "my-client",
        Version: "1.0.0",
    }, nil)
    
    transport := &mcp.CommandTransport{
        Command: exec.Command("path/to/server"),
    }
    
    session, err := client.Connect(context.Background(), transport, nil)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()
    
    // Use session to call tools, list resources, etc.
}
```

## Creating Servers

### Server with Options

```go
server := mcp.NewServer(&mcp.Implementation{
    Name:    "my-server",
    Version: "1.0.0",
}, &mcp.ServerOptions{
    Capabilities: &mcp.ServerCapabilities{
        Tools:     &mcp.ToolsCapability{},
        Resources: &mcp.ResourcesCapability{},
        Prompts:   &mcp.PromptsCapability{},
        Logging:   &mcp.LoggingCapability{},
    },
})
```

### Server with Custom Initialization

```go
server := mcp.NewServer(info, opts)

// Add initialization hook
server.OnInitialize(func(ctx context.Context, req *mcp.InitializeRequest) error {
    log.Printf("Client connecting: %s", req.ClientInfo.Name)
    return nil
})

// Add shutdown hook
server.OnShutdown(func(ctx context.Context) error {
    log.Println("Server shutting down")
    return nil
})
```

## Creating Clients

### Client with Capabilities

```go
client := mcp.NewClient(&mcp.Implementation{
    Name:    "my-client",
    Version: "1.0.0",
}, &mcp.ClientOptions{
    Capabilities: &mcp.ClientCapabilities{
        Roots: &mcp.RootsCapability{
            ListChanged: true,
        },
        Sampling: &mcp.SamplingCapability{},
    },
})
```

### Client with Handlers

```go
client := mcp.NewClient(info, opts)

// Handle sampling requests from server
client.SetSamplingHandler(func(
    ctx context.Context,
    req *mcp.CreateMessageRequest,
) (*mcp.CreateMessageResult, error) {
    // Call your LLM here
    return &mcp.CreateMessageResult{
        Model: "gpt-4",
        Content: &mcp.TextContent{Text: "response"},
    }, nil
})

// Handle root list requests
client.SetRootListHandler(func(
    ctx context.Context,
) (*mcp.RootListResult, error) {
    return &mcp.RootListResult{
        Roots: []mcp.Root{
            {URI: "file:///home/user/workspace", Name: "Workspace"},
        },
    }, nil
})
```

## Transports

### Stdio Transport (Default)

```go
// Server
transport := &mcp.StdioTransport{}
server.Run(ctx, transport)

// Client
transport := &mcp.CommandTransport{
    Command: exec.Command("./server"),
}
client.Connect(ctx, transport, nil)
```

### HTTP with Streamable Transport

```go
// Server
handler := mcp.NewStreamableHTTPHandler(
    func(r *http.Request) *mcp.Server {
        return server  // Return your server
    },
    &mcp.StreamableHTTPOptions{
        JSONResponse: true,  // Use JSON instead of SSE
    },
)

http.Handle("/mcp", handler)
log.Fatal(http.ListenAndServe(":8080", nil))

// Client
session, err := mcp.HTTPStreamableClient(
    ctx,
    "http://localhost:8080/mcp",
    info,
    opts,
)
```

### SSE Transport (Deprecated but still supported)

```go
// Server
handler := mcp.NewSSEHandler(
    func(r *http.Request) *mcp.Server {
        return server
    },
    nil,
)

http.Handle("/sse", handler)
log.Fatal(http.ListenAndServe(":8080", nil))
```

### In-Memory Transport (Testing)

```go
// Create linked transports
clientTransport, serverTransport := mcp.NewInMemoryTransports()

// Start server
go func() {
    if err := server.Run(ctx, serverTransport); err != nil {
        log.Println("Server error:", err)
    }
}()

// Connect client
session, err := client.Connect(ctx, clientTransport, nil)
```

### WebSocket Transport

```go
// Server
import (
    "github.com/gorilla/websocket"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

upgrader := websocket.Upgrader{
    Subprotocols: []string{"mcp"},
    CheckOrigin: func(r *http.Request) bool {
        return true  // Configure appropriately for production
    },
}

http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("Upgrade failed: %v", err)
        return
    }
    defer conn.Close()
    
    // Wrap connection and serve
    transport := &websocketServerTransport{conn: conn}
    if err := server.Run(r.Context(), transport); err != nil {
        log.Printf("Server error: %v", err)
    }
})

log.Fatal(http.ListenAndServe(":8080", nil))

// Client
transport := &mcp.WebSocketClientTransport{
    URL: "ws://localhost:8080/mcp",
}

session, err := client.Connect(ctx, transport, nil)
if err != nil {
    log.Fatal(err)
}
defer session.Close()
```

See `examples/server/websocket/` and `examples/client/websocket/` for complete examples.

## Tools

### Simple Tool

```go
type GreetInput struct {
    Name string `json:"name" jsonschema:"description=Name to greet"`
}

type GreetOutput struct {
    Message string `json:"message"`
}

func greetHandler(
    ctx context.Context,
    req *mcp.CallToolRequest,
    input GreetInput,
) (*mcp.CallToolResult, GreetOutput, error) {
    return nil, GreetOutput{
        Message: fmt.Sprintf("Hello, %s!", input.Name),
    }, nil
}

mcp.AddTool(server, &mcp.Tool{
    Name:        "greet",
    Description: "Greets a person by name",
}, greetHandler)
```

### Tool with Validation

```go
type MathInput struct {
    A int `json:"a" jsonschema:"description=First number,minimum=0"`
    B int `json:"b" jsonschema:"description=Second number,minimum=0"`
}

func addHandler(
    ctx context.Context,
    req *mcp.CallToolRequest,
    input MathInput,
) (*mcp.CallToolResult, int, error) {
    return nil, input.A + input.B, nil
}

mcp.AddTool(server, &mcp.Tool{
    Name:        "add",
    Description: "Adds two numbers",
}, addHandler)
```

### Tool with Progress

```go
func longRunningHandler(
    ctx context.Context,
    req *mcp.CallToolRequest,
    input struct{},
) (*mcp.CallToolResult, string, error) {
    session := mcp.SessionFromContext(ctx)
    
    // Send progress updates
    for i := 0; i < 10; i++ {
        session.NotifyProgress(req.ProgressToken, i, 10, "Processing...")
        time.Sleep(time.Second)
    }
    
    return nil, "Done!", nil
}
```

### Tool with Multiple Content Types

```go
func analyzeHandler(
    ctx context.Context,
    req *mcp.CallToolRequest,
    input struct{ Data string },
) (*mcp.CallToolResult, struct{}, error) {
    result := &mcp.CallToolResult{
        Content: []mcp.Content{
            &mcp.TextContent{Text: "Analysis complete"},
            &mcp.ImageContent{
                Data:     base64.StdEncoding.EncodeToString(imageData),
                MimeType: "image/png",
            },
        },
    }
    return result, struct{}{}, nil
}
```

### Calling Tools from Client

```go
result, err := session.CallTool(ctx, &mcp.CallToolRequest{
    Name: "greet",
    Arguments: map[string]any{
        "name": "Alice",
    },
})
if err != nil {
    log.Fatal(err)
}

// Access result
for _, content := range result.Content {
    if text, ok := content.(*mcp.TextContent); ok {
        fmt.Println(text.Text)
    }
}
```

## Resources

### Static Resources

```go
server.SetResourceListHandler(func(
    ctx context.Context,
    req *mcp.ListResourcesRequest,
) (*mcp.ListResourcesResult, error) {
    return &mcp.ListResourcesResult{
        Resources: []mcp.Resource{
            {
                URI:         "file:///data.txt",
                Name:        "Data File",
                Description: "Sample data",
                MimeType:    "text/plain",
            },
        },
    }, nil
})

server.SetResourceReadHandler(func(
    ctx context.Context,
    req *mcp.ReadResourceRequest,
) (*mcp.ReadResourceResult, error) {
    if req.URI == "file:///data.txt" {
        return &mcp.ReadResourceResult{
            Contents: []mcp.ResourceContents{
                &mcp.TextResourceContents{
                    URI:      req.URI,
                    MimeType: "text/plain",
                    Text:     "Hello, World!",
                },
            },
        }, nil
    }
    return nil, fmt.Errorf("resource not found")
})
```

### Resource Templates

```go
server.SetResourceTemplateListHandler(func(
    ctx context.Context,
    req *mcp.ListResourceTemplatesRequest,
) (*mcp.ListResourceTemplatesResult, error) {
    return &mcp.ListResourceTemplatesResult{
        ResourceTemplates: []mcp.ResourceTemplate{
            {
                URITemplate: "file:///{path}",
                Name:        "File",
                Description: "Read any file",
                MimeType:    "text/plain",
            },
        },
    }, nil
})
```

### Resource Subscriptions

```go
server.SetResourceSubscribeHandler(func(
    ctx context.Context,
    req *mcp.SubscribeRequest,
) error {
    // Track subscription
    subscriptions[req.URI] = true
    return nil
})

// Notify subscribers of changes
session := mcp.SessionFromContext(ctx)
session.NotifyResourceUpdated("file:///data.txt")
```

### Reading Resources from Client

```go
result, err := session.ReadResource(ctx, &mcp.ReadResourceRequest{
    URI: "file:///data.txt",
})
if err != nil {
    log.Fatal(err)
}

for _, content := range result.Contents {
    if text, ok := content.(*mcp.TextResourceContents); ok {
        fmt.Println(text.Text)
    }
}
```

## Prompts

### Simple Prompt

```go
server.SetPromptListHandler(func(
    ctx context.Context,
    req *mcp.ListPromptsRequest,
) (*mcp.ListPromptsResult, error) {
    return &mcp.ListPromptsResult{
        Prompts: []mcp.Prompt{
            {
                Name:        "code-review",
                Description: "Review code for issues",
                Arguments: []mcp.PromptArgument{
                    {
                        Name:        "code",
                        Description: "Code to review",
                        Required:    true,
                    },
                },
            },
        },
    }, nil
})

server.SetPromptGetHandler(func(
    ctx context.Context,
    req *mcp.GetPromptRequest,
) (*mcp.GetPromptResult, error) {
    if req.Name == "code-review" {
        code := req.Arguments["code"].(string)
        return &mcp.GetPromptResult{
            Messages: []mcp.PromptMessage{
                {
                    Role: mcp.RoleUser,
                    Content: &mcp.TextContent{
                        Text: fmt.Sprintf("Review this code:\n\n%s", code),
                    },
                },
            },
        }, nil
    }
    return nil, fmt.Errorf("prompt not found")
})
```

### Using Prompts from Client

```go
result, err := session.GetPrompt(ctx, &mcp.GetPromptRequest{
    Name: "code-review",
    Arguments: map[string]any{
        "code": "func main() { ... }",
    },
})
if err != nil {
    log.Fatal(err)
}

for _, msg := range result.Messages {
    fmt.Printf("%s: %v\n", msg.Role, msg.Content)
}
```

## Authentication

### OAuth Server with Token Verification

```go
import "github.com/modelcontextprotocol/go-sdk/auth"

// Verify tokens in middleware
func authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := r.Header.Get("Authorization")
        if token == "" {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        
        claims, err := auth.VerifyToken(token, publicKey)
        if err != nil {
            http.Error(w, "invalid token", http.StatusUnauthorized)
            return
        }
        
        // Add claims to context
        ctx := context.WithValue(r.Context(), "claims", claims)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// Use middleware
http.Handle("/mcp", authMiddleware(mcpHandler))
```

### OAuth Client

```go
import "github.com/modelcontextprotocol/go-sdk/auth"

oauthClient := &auth.Client{
    ClientID:     "my-client-id",
    ClientSecret: "my-client-secret",
    TokenURL:     "https://auth.example.com/token",
    AuthURL:      "https://auth.example.com/authorize",
    RedirectURL:  "http://localhost:8080/callback",
    Scopes:       []string{"read", "write"},
}

// Get authorization URL
authURL := oauthClient.AuthCodeURL("state-value")
fmt.Println("Visit:", authURL)

// Exchange code for token
token, err := oauthClient.Exchange(ctx, code)
if err != nil {
    log.Fatal(err)
}

// Use token in requests
req.Header.Set("Authorization", "Bearer "+token.AccessToken)
```

## Testing

### Unit Test with In-Memory Transport

```go
func TestGreetTool(t *testing.T) {
    // Setup
    server := createTestServer()
    client := createTestClient()
    
    clientTrans, serverTrans := mcp.NewInMemoryTransports()
    
    // Start server
    serverDone := make(chan error, 1)
    go func() {
        serverDone <- server.Run(context.Background(), serverTrans)
    }()
    
    // Connect client
    session, err := client.Connect(context.Background(), clientTrans, nil)
    if err != nil {
        t.Fatal(err)
    }
    defer client.Close()
    
    // Test
    result, err := session.CallTool(context.Background(), &mcp.CallToolRequest{
        Name: "greet",
        Arguments: map[string]any{"name": "Test"},
    })
    if err != nil {
        t.Fatal(err)
    }
    
    // Verify
    if len(result.Content) == 0 {
        t.Fatal("expected content")
    }
}
```

### Integration Test with HTTP

```go
func TestHTTPServer(t *testing.T) {
    // Start server
    handler := mcp.NewStreamableHTTPHandler(getServer, nil)
    server := httptest.NewServer(handler)
    defer server.Close()
    
    // Connect client
    session, err := mcp.HTTPStreamableClient(
        context.Background(),
        server.URL,
        clientInfo,
        nil,
    )
    if err != nil {
        t.Fatal(err)
    }
    
    // Test functionality
    tools, err := session.ListTools(context.Background(), nil)
    if err != nil {
        t.Fatal(err)
    }
    
    if len(tools.Tools) == 0 {
        t.Fatal("expected tools")
    }
}
```

### Mock Handler for Testing

```go
func TestClientWithMockServer(t *testing.T) {
    client := mcp.NewClient(info, nil)
    
    // Create mock server that responds to specific requests
    mockHandler := func(ctx context.Context, req *jsonrpc.Request) (any, error) {
        switch req.Method {
        case "tools/list":
            return &mcp.ListToolsResult{
                Tools: []mcp.Tool{
                    {Name: "test-tool"},
                },
            }, nil
        default:
            return nil, fmt.Errorf("unknown method: %s", req.Method)
        }
    }
    
    // ... use mock handler in test
}
```

## Common Patterns

### Error Handling

```go
// Return structured errors
if err != nil {
    return nil, &jsonrpc.Error{
        Code:    jsonrpc.CodeInvalidParams,
        Message: "invalid input",
        Data:    map[string]any{"field": "name"},
    }
}

// Check for specific error codes
if rpcErr, ok := err.(*jsonrpc.Error); ok {
    if rpcErr.Code == jsonrpc.CodeMethodNotFound {
        // Handle method not found
    }
}
```

### Context and Cancellation

```go
// Respect context cancellation
func handler(ctx context.Context, ...) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        // Do work
    }
}

// Set timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

result, err := session.CallTool(ctx, req)
```

### Logging

```go
// Server-side logging
session := mcp.SessionFromContext(ctx)
session.LogInfo("Processing request")
session.LogError("Error occurred")

// Client-side: handle log notifications
client.SetLogHandler(func(ctx context.Context, req *mcp.LoggingMessageNotification) error {
    log.Printf("[%s] %s", req.Level, req.Data)
    return nil
})
```

### Middleware

```go
// Request logging middleware
func loggingMiddleware(next mcp.Handler) mcp.Handler {
    return mcp.HandlerFunc(func(ctx context.Context, req *jsonrpc.Request) (any, error) {
        log.Printf("Request: %s", req.Method)
        result, err := next.Handle(ctx, req)
        if err != nil {
            log.Printf("Error: %v", err)
        }
        return result, err
    })
}

// Use middleware
server.Use(loggingMiddleware)
```

### Resource Cleanup

```go
// Ensure cleanup on shutdown
server.OnShutdown(func(ctx context.Context) error {
    // Close database connections
    db.Close()
    
    // Cancel background tasks
    cancel()
    
    // Wait for goroutines
    wg.Wait()
    
    return nil
})
```

## Tips and Best Practices

1. **Always handle context cancellation** - Check `ctx.Done()` in long-running operations
2. **Use type-safe tool binding** - Leverage Go generics for better type safety
3. **Provide good error messages** - Include context about what went wrong
4. **Test with in-memory transport** - Fast and reliable for unit tests
5. **Use structured output** - Return proper JSON structures, not just strings
6. **Implement progress notifications** - For long-running operations
7. **Handle cleanup properly** - Use defer and OnShutdown handlers
8. **Document your tools** - Good descriptions help AI models use them correctly
9. **Version your implementation** - Track changes to your server/client
10. **Enable logging** - Helps debug integration issues

## Additional Resources

- [Full Documentation](docs/README.md)
- [Examples Directory](examples/)
- [Design Document](design/design.md)
- [MCP Specification](https://modelcontextprotocol.io/specification/2025-03-26)

---

This quick reference covers the most common patterns. For advanced usage, refer to the full documentation and examples.
