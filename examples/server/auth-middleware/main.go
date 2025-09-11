// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This example demonstrates how to integrate auth.RequireBearerToken middleware
// with an MCP server to provide authenticated access to MCP tools and resources.

var httpAddr = flag.String("http", ":8080", "HTTP address to listen on")

// JWTClaims represents the claims in our JWT tokens.
// In a real application, you would include additional claims like issuer, audience, etc.
type JWTClaims struct {
	UserID string   `json:"user_id"` // User identifier
	Scopes []string `json:"scopes"`  // Permissions/roles for the user
	jwt.RegisteredClaims
}

// APIKey represents an API key with associated scopes.
// In production, this would be stored in a database with additional metadata.
type APIKey struct {
	Key    string   `json:"key"`     // The actual API key value
	UserID string   `json:"user_id"` // User identifier
	Scopes []string `json:"scopes"`  // Permissions/roles for this key
}

// In-memory storage for API keys (in production, use a database).
// This is for demonstration purposes only.
var apiKeys = map[string]*APIKey{
	"sk-1234567890abcdef": {
		Key:    "sk-1234567890abcdef",
		UserID: "user1",
		Scopes: []string{"read", "write"},
	},
	"sk-abcdef1234567890": {
		Key:    "sk-abcdef1234567890",
		UserID: "user2",
		Scopes: []string{"read"},
	},
}

// JWT secret (in production, use environment variables).
// This should be a strong, randomly generated secret in real applications.
var jwtSecret = []byte("your-secret-key")

// generateToken creates a JWT token for testing purposes.
// In a real application, this would be handled by your authentication service.
func generateToken(userID string, scopes []string, expiresIn time.Duration) (string, error) {
	// Create JWT claims with user information and scopes.
	claims := JWTClaims{
		UserID: userID,
		Scopes: scopes,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)), // Token expiration
			IssuedAt:  jwt.NewNumericDate(time.Now()),                // Token issuance time
			NotBefore: jwt.NewNumericDate(time.Now()),                // Token validity start time
		},
	}

	// Create and sign the JWT token.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret)
}

// verifyJWT verifies JWT tokens and returns TokenInfo for the auth middleware.
// This function implements the TokenVerifier interface required by auth.RequireBearerToken.
func verifyJWT(ctx context.Context, tokenString string, _ *http.Request) (*auth.TokenInfo, error) {
	// Parse and validate the JWT token.
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (any, error) {
		// Verify the signing method is HMAC.
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
	})
	if err != nil {
		// Return standard error for invalid tokens.
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}

	// Extract claims and verify token validity.
	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return &auth.TokenInfo{
			Scopes:     claims.Scopes,         // User permissions
			Expiration: claims.ExpiresAt.Time, // Token expiration time
		}, nil
	}

	return nil, fmt.Errorf("%w: invalid token claims", auth.ErrInvalidToken)
}

// verifyAPIKey verifies API keys and returns TokenInfo for the auth middleware.
// This function implements the TokenVerifier interface required by auth.RequireBearerToken.
func verifyAPIKey(ctx context.Context, apiKey string, _ *http.Request) (*auth.TokenInfo, error) {
	// Look up the API key in our storage.
	key, exists := apiKeys[apiKey]
	if !exists {
		return nil, fmt.Errorf("%w: API key not found", auth.ErrInvalidToken)
	}

	// API keys don't expire in this example, but you could add expiration logic here.
	// For demonstration, we set a 24-hour expiration.
	return &auth.TokenInfo{
		Scopes:     key.Scopes,                     // User permissions
		Expiration: time.Now().Add(24 * time.Hour), // 24 hour expiration
	}, nil
}

// MCP Tool Arguments
type getUserInfoArgs struct {
	UserID string `json:"user_id" jsonschema:"the user ID to get information for"`
}

type createResourceArgs struct {
	Name        string `json:"name" jsonschema:"the name of the resource"`
	Description string `json:"description" jsonschema:"the description of the resource"`
	Content     string `json:"content" jsonschema:"the content of the resource"`
}

// SayHi is a simple MCP tool that requires authentication
func SayHi(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
	// Extract user information from request (v0.3.0+)
	userInfo := req.Extra.TokenInfo

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Hello! You have scopes: %v", userInfo.Scopes)},
		},
	}, nil, nil
}

// GetUserInfo is an MCP tool that requires read scope
func GetUserInfo(ctx context.Context, req *mcp.CallToolRequest, args getUserInfoArgs) (*mcp.CallToolResult, any, error) {
	// Extract user information from request (v0.3.0+)
	userInfo := req.Extra.TokenInfo

	// Check if user has read scope.
	if !slices.Contains(userInfo.Scopes, "read") {
		return nil, nil, fmt.Errorf("insufficient permissions: read scope required")
	}

	userData := map[string]any{
		"requested_user_id": args.UserID,
		"your_scopes":       userInfo.Scopes,
		"message":           "User information retrieved successfully",
	}

	userDataJSON, err := json.Marshal(userData)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal user data: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(userDataJSON)},
		},
	}, nil, nil
}

// CreateResource is an MCP tool that requires write scope
func CreateResource(ctx context.Context, req *mcp.CallToolRequest, args createResourceArgs) (*mcp.CallToolResult, any, error) {
	// Extract user information from request (v0.3.0+)
	userInfo := req.Extra.TokenInfo

	// Check if user has write scope.
	if !slices.Contains(userInfo.Scopes, "write") {
		return nil, nil, fmt.Errorf("insufficient permissions: write scope required")
	}

	resourceInfo := map[string]any{
		"name":        args.Name,
		"description": args.Description,
		"content":     args.Content,
		"created_by":  "authenticated_user",
		"created_at":  time.Now().Format(time.RFC3339),
	}

	resourceInfoJSON, err := json.Marshal(resourceInfo)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal resource info: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Resource created successfully: %s", string(resourceInfoJSON))},
		},
	}, nil, nil
}

// createMCPServer creates an MCP server with authentication-aware tools
func createMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "authenticated-mcp-server"}, nil)

	// Add tools that require authentication.
	mcp.AddTool(server, &mcp.Tool{
		Name:        "say_hi",
		Description: "A simple greeting tool that requires authentication",
	}, SayHi)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user_info",
		Description: "Get user information (requires read scope)",
	}, GetUserInfo)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_resource",
		Description: "Create a new resource (requires write scope)",
	}, CreateResource)

	return server
}

func main() {
	flag.Parse()

	// Create the MCP server.
	server := createMCPServer()

	// Create authentication middleware.
	jwtAuth := auth.RequireBearerToken(verifyJWT, &auth.RequireBearerTokenOptions{
		Scopes: []string{"read"}, // Require "read" permission
	})

	apiKeyAuth := auth.RequireBearerToken(verifyAPIKey, &auth.RequireBearerTokenOptions{
		Scopes: []string{"read"}, // Require "read" permission
	})

	// Create HTTP handler with authentication.
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return server
	}, nil)

	// Apply authentication middleware to the MCP handler.
	authenticatedHandler := jwtAuth(handler)
	apiKeyHandler := apiKeyAuth(handler)

	// Create router for different authentication methods.
	http.HandleFunc("/mcp/jwt", authenticatedHandler.ServeHTTP)
	http.HandleFunc("/mcp/apikey", apiKeyHandler.ServeHTTP)

	// Add utility endpoints for token generation.
	http.HandleFunc("/generate-token", func(w http.ResponseWriter, r *http.Request) {
		// Get user ID from query parameters (default: "test-user").
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			userID = "test-user"
		}

		// Get scopes from query parameters (default: ["read", "write"]).
		scopes := strings.Split(r.URL.Query().Get("scopes"), ",")
		if len(scopes) == 1 && scopes[0] == "" {
			scopes = []string{"read", "write"}
		}

		// Get expiration time from query parameters (default: 1 hour).
		expiresIn := 1 * time.Hour
		if expStr := r.URL.Query().Get("expires_in"); expStr != "" {
			if exp, err := time.ParseDuration(expStr); err == nil {
				expiresIn = exp
			}
		}

		// Generate the JWT token.
		token, err := generateToken(userID, scopes, expiresIn)
		if err != nil {
			http.Error(w, "Failed to generate token", http.StatusInternalServerError)
			return
		}

		// Return the generated token.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"token": token,
			"type":  "Bearer",
		})
	})

	http.HandleFunc("/generate-api-key", func(w http.ResponseWriter, r *http.Request) {
		// Generate a random API key using cryptographically secure random bytes.
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			http.Error(w, "Failed to generate random bytes", http.StatusInternalServerError)
			return
		}
		apiKey := "sk-" + base64.URLEncoding.EncodeToString(bytes)

		// Get user ID from query parameters (default: "test-user").
		userID := r.URL.Query().Get("user_id")
		if userID == "" {
			userID = "test-user"
		}

		// Get scopes from query parameters (default: ["read"]).
		scopes := strings.Split(r.URL.Query().Get("scopes"), ",")
		if len(scopes) == 1 && scopes[0] == "" {
			scopes = []string{"read"}
		}

		// Store the new API key in our in-memory storage.
		// In production, this would be stored in a database.
		apiKeys[apiKey] = &APIKey{
			Key:    apiKey,
			UserID: userID,
			Scopes: scopes,
		}

		// Return the generated API key.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"api_key": apiKey,
			"type":    "Bearer",
		})
	})

	// Health check endpoint.
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "healthy",
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	// Start the HTTP server.
	log.Println("Authenticated MCP Server")
	log.Println("========================")
	log.Println("Server starting on", *httpAddr)
	log.Println()
	log.Println("Available endpoints:")
	log.Println("  GET  /health                    - Health check (no auth)")
	log.Println("  GET  /generate-token            - Generate JWT token")
	log.Println("  POST /generate-api-key          - Generate API key")
	log.Println("  POST /mcp/jwt                   - MCP endpoint (JWT auth)")
	log.Println("  POST /mcp/apikey                - MCP endpoint (API key auth)")
	log.Println()
	log.Println("Available MCP Tools:")
	log.Println("  - say_hi                        - Simple greeting (any auth)")
	log.Println("  - get_user_info                 - Get user info (read scope)")
	log.Println("  - create_resource               - Create resource (write scope)")
	log.Println()
	log.Println("Example usage:")
	log.Println("  # Generate a token")
	log.Println("  curl 'http://localhost:8080/generate-token?user_id=alice&scopes=read,write'")
	log.Println()
	log.Println("  # Use MCP with JWT authentication")
	log.Println("  curl -H 'Authorization: Bearer <token>' -H 'Content-Type: application/json' \\")
	log.Println("       -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"say_hi\",\"arguments\":{}}}' \\")
	log.Println("       http://localhost:8080/mcp/jwt")
	log.Println()
	log.Println("  # Generate an API key")
	log.Println("  curl -X POST 'http://localhost:8080/generate-api-key?user_id=bob&scopes=read'")
	log.Println()
	log.Println("  # Use MCP with API key authentication")
	log.Println("  curl -H 'Authorization: Bearer <api_key>' -H 'Content-Type: application/json' \\")
	log.Println("       -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"get_user_info\",\"arguments\":{\"user_id\":\"test\"}}}' \\")
	log.Println("       http://localhost:8080/mcp/apikey")

	log.Fatal(http.ListenAndServe(*httpAddr, nil))
}
