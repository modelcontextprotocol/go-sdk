// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

// Simple MCP client example with OAuth authentication support.
//
// This client connects to an MCP server using streamable HTTP or SSE transport.
//
// Usage:
//
//	go run main.go
//
// Environment variables:
//
//	MCP_SERVER_PORT - Server port (default: 8000)
//	MCP_TRANSPORT_TYPE - Transport type: streamable-http (default) or sse
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// registerClient performs Dynamic Client Registration (RFC 7591) with the authorization server.
// Returns the client ID and client secret.
func registerClient(ctx context.Context, authServerURL, redirectURI string, authMeta *oauthex.AuthServerMeta) (clientID, clientSecret string, err error) {
	clientMeta := &oauthex.ClientRegistrationMetadata{
		ClientName:              "Simple Auth Client",
		RedirectURIs:            []string{redirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "client_secret_post",
		Scope:                   "user",
	}

	registrationEndpoint := authMeta.RegistrationEndpoint
	if registrationEndpoint == "" {
		// Fallback to default registration endpoint if not in metadata
		registrationEndpoint = authServerURL + "/register"
	}

	fmt.Printf("Registering client at %s\n", registrationEndpoint)
	clientInfo, err := oauthex.RegisterClient(ctx, registrationEndpoint, clientMeta, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to register client: %w", err)
	}

	fmt.Printf("Client registered with ID: %s\n", clientInfo.ClientID)
	return clientInfo.ClientID, clientInfo.ClientSecret, nil
}

// CallbackServer handles OAuth callbacks on a local HTTP server.
type CallbackServer struct {
	port   int
	server *http.Server

	mu             sync.Mutex
	code           string
	state          string
	err            error
	resultReceived chan struct{}
}

// NewCallbackServer creates a new callback server on the specified port.
func NewCallbackServer(port int) *CallbackServer {
	return &CallbackServer{
		port:           port,
		resultReceived: make(chan struct{}),
	}
}

// Start starts the callback server.
func (s *CallbackServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	go func() {
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Callback server error: %v", err)
		}
	}()

	fmt.Printf("Started callback server on http://localhost:%d\n", s.port)
	return nil
}

// handleCallback handles the OAuth callback.
func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := r.URL.Query()

	if code := query.Get("code"); code != "" {
		s.code = code
		s.state = query.Get("state")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Authorization Successful</title></head>
<body>
	<h1>Authorization Successful!</h1>
	<p>You can close this window and return to the terminal.</p>
	<script>setTimeout(() => window.close(), 2000);</script>
</body>
</html>
`))
		close(s.resultReceived)
	} else if errMsg := query.Get("error"); errMsg != "" {
		s.err = fmt.Errorf("authorization error: %s", errMsg)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><title>Authorization Failed</title></head>
<body>
	<h1>Authorization Failed</h1>
	<p>Error: %s</p>
	<p>You can close this window and return to the terminal.</p>
</body>
</html>
`, errMsg)))
		close(s.resultReceived)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

// WaitForCallback waits for the OAuth callback with a timeout.
func (s *CallbackServer) WaitForCallback(timeout time.Duration) (code, state string, err error) {
	select {
	case <-s.resultReceived:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.code, s.state, s.err
	case <-time.After(timeout):
		return "", "", fmt.Errorf("timeout waiting for OAuth callback")
	}
}

// Stop stops the callback server.
func (s *CallbackServer) Stop() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

// AuthClient is a simple MCP client.
type AuthClient struct {
	transport mcp.Transport
	session   *mcp.ClientSession
}

// NewAuthClient creates a new client with the given transport.
func NewAuthClient(transport mcp.Transport) *AuthClient {
	return &AuthClient{
		transport: transport,
	}
}

// Connect connects to the MCP server.
func (c *AuthClient) Connect(ctx context.Context) error {
	fmt.Println("Connecting to MCP server...")

	// Create MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "simple-auth-client",
		Version: "v1.0.0",
	}, nil)

	// Connect to server
	session, err := client.Connect(ctx, c.transport, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	c.session = session
	fmt.Println("Connected to MCP server")

	return nil
}

// ListTools lists available tools from the server.
func (c *AuthClient) ListTools(ctx context.Context) error {
	if c.session == nil {
		return fmt.Errorf("not connected to server")
	}

	fmt.Println("\nAvailable tools:")
	count := 0
	for tool, err := range c.session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("failed to list tools: %w", err)
		}
		count++
		fmt.Printf("%d. %s", count, tool.Name)
		if tool.Description != "" {
			fmt.Printf("\n   Description: %s", tool.Description)
		}
		fmt.Println()
	}

	if count == 0 {
		fmt.Println("No tools available")
	}

	return nil
}

// CallTool calls a specific tool.
func (c *AuthClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) error {
	if c.session == nil {
		return fmt.Errorf("not connected to server")
	}

	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	})
	if err != nil {
		return fmt.Errorf("failed to call tool '%s': %w", toolName, err)
	}

	fmt.Printf("\nTool '%s' result:\n", toolName)
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			fmt.Println(textContent.Text)
		} else {
			fmt.Printf("%+v\n", content)
		}
	}

	return nil
}

// InteractiveLoop runs the interactive command loop.
func (c *AuthClient) InteractiveLoop(ctx context.Context) error {
	fmt.Println("\nInteractive MCP Client")
	fmt.Println("Commands:")
	fmt.Println("  list - List available tools")
	fmt.Println("  call <tool_name> [args] - Call a tool")
	fmt.Println("  quit - Exit the client")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("mcp> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if line == "quit" {
			fmt.Println("\nGoodbye!")
			break
		}

		if line == "list" {
			if err := c.ListTools(ctx); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			continue
		}

		if strings.HasPrefix(line, "call ") {
			parts := strings.SplitN(line, " ", 3)
			if len(parts) < 2 {
				fmt.Println("Please specify a tool name")
				continue
			}

			toolName := parts[1]
			var arguments map[string]any

			if len(parts) > 2 {
				if err := json.Unmarshal([]byte(parts[2]), &arguments); err != nil {
					fmt.Printf("Invalid arguments format (expected JSON): %v\n", err)
					continue
				}
			}

			if err := c.CallTool(ctx, toolName, arguments); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			continue
		}

		fmt.Println("Unknown command. Try 'list', 'call <tool_name>', or 'quit'")
	}

	return nil
}

func main() {
	// Get configuration from environment
	serverPort := os.Getenv("MCP_SERVER_PORT")
	if serverPort == "" {
		serverPort = "8000"
	}

	transportType := os.Getenv("MCP_TRANSPORT_TYPE")
	if transportType == "" {
		transportType = "streamable-http"
	}

	// Build server URL
	var serverURL string
	if transportType == "sse" {
		serverURL = fmt.Sprintf("http://localhost:%s/sse", serverPort)
	} else {
		serverURL = fmt.Sprintf("http://localhost:%s/mcp", serverPort)
	}

	fmt.Println("Simple MCP Auth Client")
	fmt.Printf("Connecting to: %s\n", serverURL)
	fmt.Printf("Transport type: %s\n", transportType)

	ctx := context.Background()

	// Create MCP transport
	var transport mcp.Transport
	if transportType == "sse" {
		transport = &mcp.SSEClientTransport{
			Endpoint: serverURL,
		}
	} else {
		transport = &mcp.StreamableClientTransport{
			Endpoint: serverURL,
		}
	}

	// Create and connect client
	client := NewAuthClient(transport)
	if err := client.Connect(ctx); err != nil {
		log.Fatalf("Connection failed: %v", err)
	}
	defer client.session.Close()

	// Run interactive loop
	if err := client.InteractiveLoop(ctx); err != nil {
		log.Fatalf("Interactive loop failed: %v", err)
	}
}
