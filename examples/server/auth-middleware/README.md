# MCP Server with Auth Middleware

This example demonstrates how to integrate the Go MCP SDK's `auth.RequireBearerToken` middleware with an MCP server to provide authenticated access to MCP tools and resources.

## Features

The server provides authentication and authorization capabilities for MCP tools:

### 1. Authentication Methods

- **JWT Token Authentication**: JSON Web Token-based authentication
- **API Key Authentication**: API key-based authentication
- **Scope-based Access Control**: Permission-based access to MCP tools

### 2. MCP Integration

- **Authenticated MCP Tools**: Tools that require authentication and check permissions
- **Token Generation**: Utility endpoints for generating test tokens
- **Middleware Integration**: Seamless integration with MCP server handlers

## Setup

```bash
cd examples/server/auth-middleware
go mod tidy
go run main.go
```

## Testing

```bash
# Run all tests
go test -v

# Run benchmark tests
go test -bench=.

# Generate coverage report
go test -cover
```

## Endpoints

### Public Endpoints (No Authentication Required)

- `GET /health` - Health check

### MCP Endpoints (Authentication Required)

- `POST /mcp/jwt` - MCP server with JWT authentication
- `POST /mcp/apikey` - MCP server with API key authentication

### Utility Endpoints

- `GET /generate-token` - Generate JWT token
- `POST /generate-api-key` - Generate API key

## Available MCP Tools

The server provides three authenticated MCP tools:

### 1. Say Hi (`say_hi`)

A simple greeting tool that requires authentication.

**Parameters:**
- None required

**Required Scopes:**
- Any authenticated user

### 2. Get User Info (`get_user_info`)

Retrieves user information based on the provided user ID.

**Parameters:**
- `user_id` (string): The user ID to get information for

**Required Scopes:**
- `read` permission

### 3. Create Resource (`create_resource`)

Creates a new resource with the provided details.

**Parameters:**
- `name` (string): The name of the resource
- `description` (string): The description of the resource
- `content` (string): The content of the resource

**Required Scopes:**
- `write` permission

## Example Usage

### 1. Generating JWT Token and Using MCP Tools

```bash
# Generate a token
curl 'http://localhost:8080/generate-token?user_id=alice&scopes=read,write'

# Use MCP tool with JWT authentication
curl -H 'Authorization: Bearer <generated_token>' \
     -H 'Content-Type: application/json' \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"say_hi","arguments":{}}}' \
     http://localhost:8080/mcp/jwt
```

### 2. Generating API Key and Using MCP Tools

```bash
# Generate an API key
curl -X POST 'http://localhost:8080/generate-api-key?user_id=bob&scopes=read'

# Use MCP tool with API key authentication
curl -H 'Authorization: Bearer <generated_api_key>' \
     -H 'Content-Type: application/json' \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_user_info","arguments":{"user_id":"test"}}}' \
     http://localhost:8080/mcp/apikey
```

### 3. Testing Scope Restrictions

```bash
# Access MCP tool requiring write scope
curl -H 'Authorization: Bearer <token_with_write_scope>' \
     -H 'Content-Type: application/json' \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_resource","arguments":{"name":"test","description":"test resource","content":"test content"}}}' \
     http://localhost:8080/mcp/jwt
```

## Core Concepts

### Authentication Integration

This example demonstrates how to integrate `auth.RequireBearerToken` middleware with an MCP server to provide authenticated access. The MCP server operates as an HTTP handler protected by authentication middleware.

### Key Features

1. **MCP Server Integration**: Create MCP server using `mcp.NewServer`
2. **Authentication Middleware**: Protect MCP handlers with `auth.RequireBearerToken`
3. **Token Verification**: Validate tokens using provided `TokenVerifier` functions
4. **Scope Checking**: Verify required permissions (scopes) are present
5. **Expiration Validation**: Check that tokens haven't expired
6. **Context Injection**: Add verified token information to request context
7. **Authenticated MCP Tools**: Tools that operate based on authentication information
8. **Error Handling**: Return appropriate HTTP status codes and error messages on authentication failure

### Implementation

```go
// Create MCP server
server := mcp.NewServer(&mcp.Implementation{Name: "authenticated-mcp-server"}, nil)

// Create authentication middleware
authMiddleware := auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
	Scopes: []string{"read", "write"},
})

// Create MCP handler
handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
	return server
}, nil)

// Apply authentication middleware to MCP handler
authenticatedHandler := authMiddleware(customMiddleware(handler))
```

### Parameters

- **verifier**: Function to verify tokens (`TokenVerifier` type)
- **opts**: Authentication options
  - `Scopes`: List of required permissions
  - `ResourceMetadataURL`: OAuth 2.0 resource metadata URL

### Error Responses

- **401 Unauthorized**: Token is invalid, expired, or missing
- **403 Forbidden**: Required scopes are insufficient
- **WWW-Authenticate Header**: Included when resource metadata URL is configured

## Implementation Details

### 1. TokenVerifier Implementation

```go
func jwtVerifier(ctx context.Context, tokenString string) (*auth.TokenInfo, error) {
	// JWT token verification logic
	// On success: Return TokenInfo
	// On failure: Return auth.ErrInvalidToken
}
```

### 2. Using Authentication Information in MCP Tools

```go
// Get authentication information in MCP tool
func MyTool(ctx context.Context, req *mcp.CallToolRequest, args MyArgs) (*mcp.CallToolResult, any, error) {
	// Extract authentication info from request
	userInfo := req.Extra.TokenInfo

	// Check scopes
	if !slices.Contains(userInfo.Scopes, "read") {
		return nil, nil, fmt.Errorf("insufficient permissions: read scope required")
	}

	// Execute tool logic
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Tool executed successfully"},
		},
	}, nil, nil
}
```

### 3. Middleware Composition

```go
// Combine authentication middleware with custom middleware
authenticatedHandler := authMiddleware(customMiddleware(mcpHandler))
```

## Security Best Practices

1. **Environment Variables**: Use environment variables for JWT secrets in production
2. **Database Storage**: Store API keys in a database
3. **HTTPS Usage**: Always use HTTPS in production environments
4. **Token Expiration**: Set appropriate token expiration times
5. **Principle of Least Privilege**: Grant only the minimum required scopes

## Use Cases

**Ideal for:**

- MCP servers requiring authentication and authorization
- Applications needing scope-based access control
- Systems requiring both JWT and API key authentication
- Projects needing secure MCP tool access
- Scenarios requiring audit trails and permission management

**Examples:**

- Enterprise MCP servers with user management
- Multi-tenant MCP applications
- Secure API gateways with MCP integration
- Development environments with authentication requirements
- Production systems requiring fine-grained access control
