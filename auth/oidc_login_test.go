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
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestInitiateOIDCLogin tests the OIDC authorization request generation.
func TestInitiateOIDCLogin(t *testing.T) {
	// Create mock IdP server
	idpServer := createMockOIDCServer(t)
	defer idpServer.Close()
	config := &OIDCLoginConfig{
		IssuerURL:   idpServer.URL,
		ClientID:    "test-client",
		RedirectURL: "http://localhost:8080/callback",
		Scopes:      []string{"openid", "profile", "email"},
		HTTPClient:  idpServer.Client(),
	}
	t.Run("successful initiation", func(t *testing.T) {
		authReq, err := InitiateOIDCLogin(context.Background(), config)
		if err != nil {
			t.Fatalf("InitiateOIDCLogin failed: %v", err)
		}
		// Validate AuthURL
		if authReq.AuthURL == "" {
			t.Error("AuthURL is empty")
		}
		// Parse and validate URL parameters
		u, err := url.Parse(authReq.AuthURL)
		if err != nil {
			t.Fatalf("Failed to parse AuthURL: %v", err)
		}
		q := u.Query()
		if q.Get("response_type") != "code" {
			t.Errorf("expected response_type 'code', got '%s'", q.Get("response_type"))
		}
		if q.Get("client_id") != "test-client" {
			t.Errorf("expected client_id 'test-client', got '%s'", q.Get("client_id"))
		}
		if q.Get("redirect_uri") != "http://localhost:8080/callback" {
			t.Errorf("expected redirect_uri 'http://localhost:8080/callback', got '%s'", q.Get("redirect_uri"))
		}
		if q.Get("scope") != "openid profile email" {
			t.Errorf("expected scope 'openid profile email', got '%s'", q.Get("scope"))
		}
		if q.Get("code_challenge_method") != "S256" {
			t.Errorf("expected code_challenge_method 'S256', got '%s'", q.Get("code_challenge_method"))
		}
		// Validate state is generated
		if authReq.State == "" {
			t.Error("State is empty")
		}
		if q.Get("state") != authReq.State {
			t.Errorf("state in URL doesn't match returned state")
		}
		// Validate PKCE parameters
		if authReq.CodeVerifier == "" {
			t.Error("CodeVerifier is empty")
		}
		if q.Get("code_challenge") == "" {
			t.Error("code_challenge is empty")
		}
	})
	t.Run("with login_hint", func(t *testing.T) {
		configWithHint := *config
		configWithHint.LoginHint = "user@example.com"
		authReq, err := InitiateOIDCLogin(context.Background(), &configWithHint)
		if err != nil {
			t.Fatalf("InitiateOIDCLogin failed: %v", err)
		}
		u, err := url.Parse(authReq.AuthURL)
		if err != nil {
			t.Fatalf("Failed to parse AuthURL: %v", err)
		}
		q := u.Query()
		if q.Get("login_hint") != "user@example.com" {
			t.Errorf("expected login_hint 'user@example.com', got '%s'", q.Get("login_hint"))
		}
	})
	t.Run("without login_hint", func(t *testing.T) {
		authReq, err := InitiateOIDCLogin(context.Background(), config)
		if err != nil {
			t.Fatalf("InitiateOIDCLogin failed: %v", err)
		}
		u, err := url.Parse(authReq.AuthURL)
		if err != nil {
			t.Fatalf("Failed to parse AuthURL: %v", err)
		}
		q := u.Query()
		if q.Has("login_hint") {
			t.Errorf("expected no login_hint parameter, but got '%s'", q.Get("login_hint"))
		}
	})
	t.Run("nil config", func(t *testing.T) {
		_, err := InitiateOIDCLogin(context.Background(), nil)
		if err == nil {
			t.Error("expected error for nil config, got nil")
		}
	})
	t.Run("missing openid scope", func(t *testing.T) {
		badConfig := *config
		badConfig.Scopes = []string{"profile", "email"} // Missing "openid"
		_, err := InitiateOIDCLogin(context.Background(), &badConfig)
		if err == nil {
			t.Error("expected error for missing openid scope, got nil")
		}
		if !strings.Contains(err.Error(), "openid") {
			t.Errorf("expected error about missing 'openid', got: %v", err)
		}
	})
	t.Run("missing required fields", func(t *testing.T) {
		tests := []struct {
			name      string
			mutate    func(*OIDCLoginConfig)
			expectErr string
		}{
			{
				name:      "missing IssuerURL",
				mutate:    func(c *OIDCLoginConfig) { c.IssuerURL = "" },
				expectErr: "IssuerURL is required",
			},
			{
				name:      "missing ClientID",
				mutate:    func(c *OIDCLoginConfig) { c.ClientID = "" },
				expectErr: "ClientID is required",
			},
			{
				name:      "missing RedirectURL",
				mutate:    func(c *OIDCLoginConfig) { c.RedirectURL = "" },
				expectErr: "RedirectURL is required",
			},
			{
				name:      "missing Scopes",
				mutate:    func(c *OIDCLoginConfig) { c.Scopes = nil },
				expectErr: "Scopes is required",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				badConfig := *config
				tt.mutate(&badConfig)
				_, err := InitiateOIDCLogin(context.Background(), &badConfig)
				if err == nil {
					t.Error("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.expectErr) {
					t.Errorf("expected error containing '%s', got: %v", tt.expectErr, err)
				}
			})
		}
	})
}

// TestCompleteOIDCLogin tests the authorization code exchange.
func TestCompleteOIDCLogin(t *testing.T) {
	// Create mock IdP server
	idpServer := createMockOIDCServerWithToken(t)
	defer idpServer.Close()
	config := &OIDCLoginConfig{
		IssuerURL:    idpServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/callback",
		Scopes:       []string{"openid", "profile", "email"},
		HTTPClient:   idpServer.Client(),
	}
	t.Run("successful code exchange", func(t *testing.T) {
		tokens, err := CompleteOIDCLogin(
			context.Background(),
			config,
			"test-auth-code",
			"test-code-verifier",
		)
		if err != nil {
			t.Fatalf("CompleteOIDCLogin failed: %v", err)
		}
		// Validate tokens
		if tokens.IDToken == "" {
			t.Error("IDToken is empty")
		}
		if tokens.AccessToken == "" {
			t.Error("AccessToken is empty")
		}
		if tokens.TokenType != "Bearer" {
			t.Errorf("expected TokenType 'Bearer', got '%s'", tokens.TokenType)
		}
		if tokens.ExpiresAt == 0 {
			t.Error("ExpiresAt is zero")
		}
	})
	t.Run("nil config", func(t *testing.T) {
		_, err := CompleteOIDCLogin(
			context.Background(),
			nil,
			"test-auth-code",
			"test-code-verifier",
		)
		if err == nil {
			t.Error("expected error for nil config, got nil")
		}
	})
	t.Run("missing parameters", func(t *testing.T) {
		tests := []struct {
			name         string
			authCode     string
			codeVerifier string
			expectErr    string
		}{
			{
				name:         "missing authCode",
				authCode:     "",
				codeVerifier: "test-verifier",
				expectErr:    "authCode is required",
			},
			{
				name:         "missing codeVerifier",
				authCode:     "test-code",
				codeVerifier: "",
				expectErr:    "codeVerifier is required",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := CompleteOIDCLogin(
					context.Background(),
					config,
					tt.authCode,
					tt.codeVerifier,
				)
				if err == nil {
					t.Error("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.expectErr) {
					t.Errorf("expected error containing '%s', got: %v", tt.expectErr, err)
				}
			})
		}
	})
}

// TestOIDCLoginE2E tests the complete OIDC login flow end-to-end.
func TestOIDCLoginE2E(t *testing.T) {
	// Create mock IdP server
	idpServer := createMockOIDCServerWithToken(t)
	defer idpServer.Close()
	config := &OIDCLoginConfig{
		IssuerURL:    idpServer.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost:8080/callback",
		Scopes:       []string{"openid", "profile", "email"},
		HTTPClient:   idpServer.Client(),
	}
	// Step 1: Initiate login
	authReq, err := InitiateOIDCLogin(context.Background(), config)
	if err != nil {
		t.Fatalf("InitiateOIDCLogin failed: %v", err)
	}
	// Step 2: Simulate user authentication and redirect
	// (In real flow, user would visit authReq.AuthURL and IdP would redirect back)
	// Here we just use a mock authorization code
	mockAuthCode := "mock-authorization-code"
	// Step 3: Complete login with authorization code
	tokens, err := CompleteOIDCLogin(
		context.Background(),
		config,
		mockAuthCode,
		authReq.CodeVerifier,
	)
	if err != nil {
		t.Fatalf("CompleteOIDCLogin failed: %v", err)
	}
	// Validate we got an ID token
	if tokens.IDToken == "" {
		t.Error("Expected ID token, got empty string")
	}
	// Validate ID token is a JWT (has 3 parts)
	parts := strings.Split(tokens.IDToken, ".")
	if len(parts) != 3 {
		t.Errorf("Expected JWT with 3 parts, got %d parts", len(parts))
	}
}

// createMockOIDCServer creates a mock OIDC server for testing InitiateOIDCLogin.
func createMockOIDCServer(t *testing.T) *httptest.Server {
	var serverURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle OIDC discovery
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/authorize",
				"token_endpoint":                   serverURL + "/token",
				"jwks_uri":                         serverURL + "/.well-known/jwks.json",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"S256"},
				"grant_types_supported":            []string{"authorization_code"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	serverURL = server.URL
	return server
}

// createMockOIDCServerWithToken creates a mock OIDC server that also handles token exchange.
func createMockOIDCServerWithToken(t *testing.T) *httptest.Server {
	var serverURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle OIDC discovery
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/authorize",
				"token_endpoint":                   serverURL + "/token",
				"jwks_uri":                         serverURL + "/.well-known/jwks.json",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"S256"},
				"grant_types_supported":            []string{"authorization_code"},
			})
			return
		}
		// Handle token endpoint
		if r.URL.Path == "/token" {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "failed to parse form", http.StatusBadRequest)
				return
			}
			// Validate grant type
			if r.FormValue("grant_type") != "authorization_code" {
				http.Error(w, "invalid grant_type", http.StatusBadRequest)
				return
			}
			// Create mock ID token (JWT)
			now := time.Now().Unix()
			idToken := fmt.Sprintf("eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.%s.mock-signature",
				base64EncodeClaims(map[string]interface{}{
					"iss":   serverURL,
					"sub":   "test-user",
					"aud":   "test-client",
					"exp":   now + 3600,
					"iat":   now,
					"email": "test@example.com",
				}))
			// Return token response
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token":  "mock-access-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "mock-refresh-token",
				"id_token":      idToken,
			})
			return
		}
		http.NotFound(w, r)
	}))
	serverURL = server.URL
	return server
}

// base64EncodeClaims encodes JWT claims for testing.
func base64EncodeClaims(claims map[string]interface{}) string {
	claimsJSON, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(claimsJSON)
}
