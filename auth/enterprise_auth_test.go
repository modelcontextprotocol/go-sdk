// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// TestEnterpriseAuthFlow tests the complete enterprise auth flow.
func TestEnterpriseAuthFlow(t *testing.T) {
	// Create test servers for IdP and MCP Server
	idpServer := createMockIdPServer(t)
	defer idpServer.Close()
	mcpServer := createMockMCPServer(t)
	defer mcpServer.Close()
	// Create a test ID Token
	idToken := createTestIDToken()
	// Configure enterprise auth
	config := &EnterpriseAuthConfig{
		IdPIssuerURL:     idpServer.URL,
		IdPClientID:      "test-idp-client",
		IdPClientSecret:  "test-idp-secret",
		MCPAuthServerURL: mcpServer.URL,
		MCPResourceURL:   "https://mcp.example.com",
		MCPClientID:      "test-mcp-client",
		MCPClientSecret:  "test-mcp-secret",
		MCPScopes:        []string{"read", "write"},
		HTTPClient:       idpServer.Client(),
	}
	// Test successful flow
	t.Run("successful flow", func(t *testing.T) {
		token, err := EnterpriseAuthFlow(context.Background(), config, idToken)
		if err != nil {
			t.Fatalf("EnterpriseAuthFlow failed: %v", err)
		}
		if token.AccessToken != "mcp-access-token" {
			t.Errorf("expected access token 'mcp-access-token', got '%s'", token.AccessToken)
		}
		if token.TokenType != "Bearer" {
			t.Errorf("expected token type 'Bearer', got '%s'", token.TokenType)
		}
	})
	// Test missing config
	t.Run("nil config", func(t *testing.T) {
		_, err := EnterpriseAuthFlow(context.Background(), nil, idToken)
		if err == nil {
			t.Error("expected error for nil config, got nil")
		}
	})
	// Test missing ID token
	t.Run("empty ID token", func(t *testing.T) {
		_, err := EnterpriseAuthFlow(context.Background(), config, "")
		if err == nil {
			t.Error("expected error for empty ID token, got nil")
		}
	})
	// Test missing IdP issuer
	t.Run("missing IdP issuer", func(t *testing.T) {
		badConfig := *config
		badConfig.IdPIssuerURL = ""
		_, err := EnterpriseAuthFlow(context.Background(), &badConfig, idToken)
		if err == nil {
			t.Error("expected error for missing IdP issuer, got nil")
		}
	})
}

// createMockIdPServer creates a mock IdP server for testing.
func createMockIdPServer(t *testing.T) *httptest.Server {
	var serverURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle OIDC discovery endpoint
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"issuer":                           serverURL, // Use actual server URL
				"token_endpoint":                   serverURL + "/oauth2/v1/token",
				"jwks_uri":                         serverURL + "/.well-known/jwks.json",
				"code_challenge_methods_supported": []string{"S256"},
				"grant_types_supported": []string{
					"authorization_code",
					"urn:ietf:params:oauth:grant-type:token-exchange",
				},
				"response_types_supported": []string{"code"},
			})
			return
		}

		// Handle token exchange endpoint
		if r.URL.Path != "/oauth2/v1/token" {
			http.NotFound(w, r)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}
		grantType := r.FormValue("grant_type")
		if grantType != oauthex.GrantTypeTokenExchange {
			http.Error(w, "invalid grant type", http.StatusBadRequest)
			return
		}

		// Return a mock ID-JAG
		now := time.Now().Unix()
		header := map[string]string{"typ": "oauth-id-jag+jwt", "alg": "RS256"}
		claims := map[string]interface{}{
			"iss":       "https://test.okta.com",
			"sub":       "test-user",
			"aud":       r.FormValue("audience"),
			"resource":  r.FormValue("resource"),
			"client_id": r.FormValue("client_id"),
			"jti":       "test-jti",
			"exp":       now + 300,
			"iat":       now,
			"scope":     r.FormValue("scope"),
		}
		headerJSON, _ := json.Marshal(header)
		claimsJSON, _ := json.Marshal(claims)
		headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
		claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
		mockIDJAG := fmt.Sprintf("%s.%s.mock-signature", headerB64, claimsB64)

		resp := oauthex.TokenExchangeResponse{
			IssuedTokenType: oauthex.TokenTypeIDJAG,
			AccessToken:     mockIDJAG,
			TokenType:       "N_A",
			ExpiresIn:       300,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	serverURL = server.URL // Capture server URL for discovery response
	return server
}

// createMockMCPServer creates a mock MCP Server for testing.
func createMockMCPServer(t *testing.T) *httptest.Server {
	var serverURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle OIDC discovery endpoint
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"issuer":                           serverURL, // Use actual server URL
				"token_endpoint":                   serverURL + "/v1/token",
				"jwks_uri":                         serverURL + "/.well-known/jwks.json",
				"code_challenge_methods_supported": []string{"S256"},
				"grant_types_supported": []string{
					"urn:ietf:params:oauth:grant-type:jwt-bearer",
				},
			})
			return
		}

		// Handle JWT Bearer endpoint
		if r.URL.Path != "/v1/token" {
			http.NotFound(w, r)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}
		grantType := r.FormValue("grant_type")
		if grantType != oauthex.GrantTypeJWTBearer {
			http.Error(w, "invalid grant type", http.StatusBadRequest)
			return
		}

		resp := oauthex.JWTBearerResponse{
			AccessToken:  "mcp-access-token",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			Scope:        "read write",
			RefreshToken: "mcp-refresh-token",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	serverURL = server.URL // Capture server URL for discovery response
	return server
}

// createTestIDToken creates a mock ID Token for testing.
func createTestIDToken() string {
	now := time.Now().Unix()
	header := map[string]string{"typ": "JWT", "alg": "RS256"}
	claims := map[string]interface{}{
		"iss":   "https://test.okta.com",
		"sub":   "test-user",
		"aud":   "test-client",
		"exp":   now + 3600,
		"iat":   now,
		"email": "test@example.com",
	}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	return fmt.Sprintf("%s.%s.mock-signature", headerB64, claimsB64)
}
