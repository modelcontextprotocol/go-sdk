// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package extauth

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

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func TestPerformOIDCLogin(t *testing.T) {
	idpServer := createMockOIDCServer(t)
	defer idpServer.Close()

	validConfig := &OIDCLoginConfig{
		IssuerURL: idpServer.URL,
		Credentials: &oauthex.ClientCredentials{
			ClientID: "test-client",
			ClientSecretAuth: &oauthex.ClientSecretAuth{
				ClientSecret: "test-secret",
			},
		},
		RedirectURL: "http://localhost:8080/callback",
		Scopes:      []string{"openid", "profile", "email"},
		HTTPClient:  idpServer.Client(),
	}

	t.Run("successful flow", func(t *testing.T) {
		token, err := PerformOIDCLogin(context.Background(), validConfig,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				u, err := url.Parse(args.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid authURL: %w", err)
				}
				q := u.Query()

				if got := q.Get("response_type"); got != "code" {
					t.Errorf("response_type = %q, want %q", got, "code")
				}
				if got := q.Get("client_id"); got != "test-client" {
					t.Errorf("client_id = %q, want %q", got, "test-client")
				}
				if got := q.Get("redirect_uri"); got != "http://localhost:8080/callback" {
					t.Errorf("redirect_uri = %q, want %q", got, "http://localhost:8080/callback")
				}
				if got := q.Get("scope"); got != "openid profile email" {
					t.Errorf("scope = %q, want %q", got, "openid profile email")
				}
				if got := q.Get("code_challenge_method"); got != "S256" {
					t.Errorf("code_challenge_method = %q, want %q", got, "S256")
				}
				if q.Get("code_challenge") == "" {
					t.Error("code_challenge is empty")
				}
				if q.Get("state") == "" {
					t.Error("state is empty")
				}

				return &auth.AuthorizationResult{
					Code:  "mock-auth-code",
					State: q.Get("state"),
				}, nil
			})

		if err != nil {
			t.Fatalf("PerformOIDCLogin() error = %v", err)
		}

		idToken, ok := token.Extra("id_token").(string)
		if !ok || idToken == "" {
			t.Error("id_token is missing or empty")
		}
		if token.AccessToken == "" {
			t.Error("AccessToken is empty")
		}
	})

	t.Run("with login_hint", func(t *testing.T) {
		configWithHint := *validConfig
		configWithHint.LoginHint = "user@example.com"

		_, err := PerformOIDCLogin(context.Background(), &configWithHint,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				u, err := url.Parse(args.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid authURL: %w", err)
				}
				if got := u.Query().Get("login_hint"); got != "user@example.com" {
					t.Errorf("login_hint = %q, want %q", got, "user@example.com")
				}
				return &auth.AuthorizationResult{
					Code:  "mock-auth-code",
					State: u.Query().Get("state"),
				}, nil
			})
		if err != nil {
			t.Fatalf("PerformOIDCLogin() error = %v", err)
		}
	})

	t.Run("without login_hint", func(t *testing.T) {
		_, err := PerformOIDCLogin(context.Background(), validConfig,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				u, err := url.Parse(args.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid authURL: %w", err)
				}
				if u.Query().Has("login_hint") {
					t.Errorf("login_hint should be absent, got %q", u.Query().Get("login_hint"))
				}
				return &auth.AuthorizationResult{
					Code:  "mock-auth-code",
					State: u.Query().Get("state"),
				}, nil
			})
		if err != nil {
			t.Fatalf("PerformOIDCLogin() error = %v", err)
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		_, err := PerformOIDCLogin(context.Background(), validConfig,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				return &auth.AuthorizationResult{
					Code:  "mock-auth-code",
					State: "wrong-state",
				}, nil
			})

		if err == nil {
			t.Fatal("expected error for state mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "state mismatch") {
			t.Errorf("error = %v, want error containing %q", err, "state mismatch")
		}
	})

	t.Run("fetcher error", func(t *testing.T) {
		_, err := PerformOIDCLogin(context.Background(), validConfig,
			func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				return nil, fmt.Errorf("user cancelled")
			})

		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "user cancelled") {
			t.Errorf("error = %v, want error containing %q", err, "user cancelled")
		}
	})

	t.Run("nil fetcher", func(t *testing.T) {
		_, err := PerformOIDCLogin(context.Background(), validConfig, nil)
		if err == nil {
			t.Fatal("expected error for nil fetcher, got nil")
		}
		if !strings.Contains(err.Error(), "authCodeFetcher is required") {
			t.Errorf("error = %v, want error containing %q", err, "authCodeFetcher is required")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		noopFetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, fmt.Errorf("should not be called")
		}
		_, err := PerformOIDCLogin(context.Background(), nil, noopFetcher)
		if err == nil {
			t.Fatal("expected error for nil config, got nil")
		}
		if !strings.Contains(err.Error(), "config is required") {
			t.Errorf("error = %v, want error containing %q", err, "config is required")
		}
	})

	t.Run("missing openid scope", func(t *testing.T) {
		badConfig := *validConfig
		badConfig.Scopes = []string{"profile", "email"}
		noopFetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, fmt.Errorf("should not be called")
		}
		_, err := PerformOIDCLogin(context.Background(), &badConfig, noopFetcher)
		if err == nil {
			t.Fatal("expected error for missing openid scope, got nil")
		}
		if !strings.Contains(err.Error(), "openid") {
			t.Errorf("error = %v, want error containing %q", err, "openid")
		}
	})

	t.Run("missing required fields", func(t *testing.T) {
		noopFetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, fmt.Errorf("should not be called")
		}
		tests := []struct {
			name    string
			mutate  func(*OIDCLoginConfig)
			wantErr string
		}{
			{
				name:    "missing IssuerURL",
				mutate:  func(c *OIDCLoginConfig) { c.IssuerURL = "" },
				wantErr: "IssuerURL is required",
			},
			{
				name:    "missing ClientID",
				mutate:  func(c *OIDCLoginConfig) { c.Credentials = &oauthex.ClientCredentials{} },
				wantErr: "ClientID is required",
			},
			{
				name: "missing RedirectURL",
				mutate: func(c *OIDCLoginConfig) {
					c.RedirectURL = ""
				},
				wantErr: "RedirectURL is required",
			},
			{
				name: "missing Scopes",
				mutate: func(c *OIDCLoginConfig) {
					c.Scopes = nil
				},
				wantErr: "at least one scope is required",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := *validConfig
				tt.mutate(&cfg)
				_, err := PerformOIDCLogin(context.Background(), &cfg, noopFetcher)
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want error containing %q", err, tt.wantErr)
				}
			})
		}
	})
}

// createMockOIDCServer creates a mock OIDC server that handles metadata
// discovery and token exchange.
func createMockOIDCServer(t *testing.T) *httptest.Server {
	t.Helper()
	var serverURL string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           serverURL,
				"authorization_endpoint":           serverURL + "/authorize",
				"token_endpoint":                   serverURL + "/token",
				"jwks_uri":                         serverURL + "/.well-known/jwks.json",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"S256"},
				"grant_types_supported":            []string{"authorization_code"},
			})

		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "failed to parse form", http.StatusBadRequest)
				return
			}
			if r.FormValue("grant_type") != "authorization_code" {
				http.Error(w, "invalid grant_type", http.StatusBadRequest)
				return
			}
			if r.FormValue("code_verifier") == "" {
				http.Error(w, "missing code_verifier", http.StatusBadRequest)
				return
			}

			now := time.Now().Unix()
			idToken := fmt.Sprintf("eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.%s.mock-signature",
				base64EncodeClaims(map[string]any{
					"iss":   serverURL,
					"sub":   "test-user",
					"aud":   "test-client",
					"exp":   now + 3600,
					"iat":   now,
					"email": "test@example.com",
				}))
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "mock-access-token",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "mock-refresh-token",
				"id_token":      idToken,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	serverURL = server.URL
	return server
}

// base64EncodeClaims encodes JWT claims for testing.
func base64EncodeClaims(claims map[string]any) string {
	claimsJSON, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(claimsJSON)
}
