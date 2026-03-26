// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package extauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// TestNewEnterpriseHandler_Validation tests validation in NewEnterpriseHandler.
func TestNewEnterpriseHandler_Validation(t *testing.T) {
	validConfig := &EnterpriseHandlerConfig{
		IdPIssuerURL: "https://idp.example.com",
		IdPCredentials: &oauthex.ClientCredentials{
			ClientID: "idp_client_id",
		},
		MCPAuthServerURL: "https://mcp-auth.example.com",
		MCPResourceURI:   "https://mcp.example.com",
		MCPCredentials: &oauthex.ClientCredentials{
			ClientID: "mcp_client_id",
		},
		IDTokenFetcher: func(ctx context.Context) (*oauth2.Token, error) {
			token := &oauth2.Token{
				AccessToken: "mock_access_token",
				TokenType:   "Bearer",
			}
			return token.WithExtra(map[string]interface{}{"id_token": "mock_id_token"}), nil
		},
	}

	tests := []struct {
		name      string
		config    *EnterpriseHandlerConfig
		wantError string
	}{
		{
			name:      "nil config",
			config:    nil,
			wantError: "config must be provided",
		},
		{
			name: "missing IdPIssuerURL",
			config: &EnterpriseHandlerConfig{
				IdPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "IdPIssuerURL is required",
		},
		{
			name: "nil IdPCredentials",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL:     "https://idp.example.com",
				IdPCredentials:   nil,
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "IdPCredentials is required",
		},
		{
			name: "invalid IdPCredentials - empty ClientID",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL: "https://idp.example.com",
				IdPCredentials: &oauthex.ClientCredentials{
					ClientID: "", // Invalid - empty
				},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "invalid IdPCredentials",
		},
		{
			name: "invalid IdPCredentials - empty ClientSecret in ClientSecretAuth",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL: "https://idp.example.com",
				IdPCredentials: &oauthex.ClientCredentials{
					ClientID: "idp_client_id",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
						ClientSecret: "", // Invalid - empty secret when ClientSecretAuth is set
					},
				},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "invalid IdPCredentials",
		},
		{
			name: "missing MCPAuthServerURL",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL:     "https://idp.example.com",
				IdPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				MCPAuthServerURL: "",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "MCPAuthServerURL is required",
		},
		{
			name: "missing MCPResourceURI",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL:     "https://idp.example.com",
				IdPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "MCPResourceURI is required",
		},
		{
			name: "nil MCPCredentials",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL:     "https://idp.example.com",
				IdPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   nil,
				IDTokenFetcher:   func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "MCPCredentials is required",
		},
		{
			name: "invalid MCPCredentials - empty ClientID",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL:     "https://idp.example.com",
				IdPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials: &oauthex.ClientCredentials{
					ClientID: "", // Invalid - empty
				},
				IDTokenFetcher: func(ctx context.Context) (*oauth2.Token, error) { return nil, nil },
			},
			wantError: "invalid MCPCredentials",
		},
		{
			name: "missing IDTokenFetcher",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL:     "https://idp.example.com",
				IdPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials:   &oauthex.ClientCredentials{ClientID: "id"},
				IDTokenFetcher:   nil,
			},
			wantError: "IDTokenFetcher is required",
		},
		{
			name:      "valid config - public clients (no ClientSecretAuth)",
			config:    validConfig,
			wantError: "",
		},
		{
			name: "valid config - confidential clients (with ClientSecretAuth)",
			config: &EnterpriseHandlerConfig{
				IdPIssuerURL: "https://idp.example.com",
				IdPCredentials: &oauthex.ClientCredentials{
					ClientID: "idp_client_id",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
						ClientSecret: "idp_secret",
					},
				},
				MCPAuthServerURL: "https://mcp-auth.example.com",
				MCPResourceURI:   "https://mcp.example.com",
				MCPCredentials: &oauthex.ClientCredentials{
					ClientID: "mcp_client_id",
					ClientSecretAuth: &oauthex.ClientSecretAuth{
						ClientSecret: "mcp_secret",
					},
				},
				IDTokenFetcher: func(ctx context.Context) (*oauth2.Token, error) {
					token := &oauth2.Token{
						AccessToken: "mock_access_token",
						TokenType:   "Bearer",
					}
					return token.WithExtra(map[string]interface{}{"id_token": "mock_id_token"}), nil
				},
			},
			wantError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := NewEnterpriseHandler(tt.config)
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantError)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if handler == nil {
					t.Fatal("expected handler to be non-nil")
				}
			}
		})
	}
}

// TestEnterpriseHandler_Authorize_E2E tests the complete enterprise authorization flow.
func TestEnterpriseHandler_Authorize_E2E(t *testing.T) {
	// Set up IdP (Identity Provider) fake server with token exchange support
	idpServer := setupIdPServer(t)

	// Set up MCP authorization server with JWT bearer grant support
	mcpAuthServer := setupMCPAuthServer(t)

	// Create enterprise handler
	handler, err := NewEnterpriseHandler(&EnterpriseHandlerConfig{
		IdPIssuerURL: idpServer.URL,
		IdPCredentials: &oauthex.ClientCredentials{
			ClientID: "idp_client_id",
			ClientSecretAuth: &oauthex.ClientSecretAuth{
				ClientSecret: "idp_secret",
			},
		},
		MCPAuthServerURL: mcpAuthServer.URL,
		MCPResourceURI:   "https://mcp.example.com",
		MCPCredentials: &oauthex.ClientCredentials{
			ClientID: "mcp_client_id",
			ClientSecretAuth: &oauthex.ClientSecretAuth{
				ClientSecret: "mcp_secret",
			},
		},
		MCPScopes: []string{"read", "write"},
		IDTokenFetcher: func(ctx context.Context) (*oauth2.Token, error) {
			token := &oauth2.Token{
				AccessToken: "mock_access_token",
				TokenType:   "Bearer",
			}
			return token.WithExtra(map[string]interface{}{"id_token": "mock_id_token_from_user_login"}), nil
		},
	})
	if err != nil {
		t.Fatalf("NewEnterpriseHandler failed: %v", err)
	}

	// Simulate a 401 response from MCP server
	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.com/api", nil)
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}

	// Perform authorization
	if err := handler.Authorize(context.Background(), req, resp); err != nil {
		t.Fatalf("Authorize failed: %v", err)
	}

	// Verify token source is set
	tokenSource, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource failed: %v", err)
	}
	if tokenSource == nil {
		t.Fatal("expected token source to be set after authorization")
	}

	// Verify we can get a token
	token, err := tokenSource.Token()
	if err != nil {
		t.Fatalf("Token() failed: %v", err)
	}
	if token.AccessToken != "mcp_access_token_from_jwt_bearer" {
		t.Errorf("unexpected access token: got %q, want %q",
			token.AccessToken, "mcp_access_token_from_jwt_bearer")
	}
}

// setupIdPServer creates a fake IdP server that supports token exchange.
func setupIdPServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	var server *httptest.Server

	// OAuth/OIDC metadata endpoint - uses closure to get server URL
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                           server.URL,
			"token_endpoint":                   server.URL + "/token",
			"authorization_endpoint":           server.URL + "/authorize",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})

	// Token endpoint - supports token exchange (RFC 8693)
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}

		grantType := r.Form.Get("grant_type")
		if grantType != oauthex.GrantTypeTokenExchange {
			http.Error(w, fmt.Sprintf("unsupported grant_type: %s", grantType), http.StatusBadRequest)
			return
		}

		// Verify client authentication
		clientID := r.Form.Get("client_id")
		clientSecret := r.Form.Get("client_secret")
		if clientID != "idp_client_id" || clientSecret != "idp_secret" {
			http.Error(w, "invalid client credentials", http.StatusUnauthorized)
			return
		}

		// Verify token exchange parameters
		if r.Form.Get("requested_token_type") != oauthex.TokenTypeIDJAG {
			http.Error(w, "invalid requested_token_type", http.StatusBadRequest)
			return
		}
		if r.Form.Get("subject_token_type") != oauthex.TokenTypeIDToken {
			http.Error(w, "invalid subject_token_type", http.StatusBadRequest)
			return
		}
		if r.Form.Get("subject_token") == "" {
			http.Error(w, "missing subject_token", http.StatusBadRequest)
			return
		}

		// Return ID-JAG (Identity Assertion JWT Authorization Grant)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":      "id-jag-token-from-idp",
			"issued_token_type": oauthex.TokenTypeIDJAG,
			"token_type":        "N_A",
			"expires_in":        300,
			"scope":             "read write",
		})
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

// setupMCPAuthServer creates a fake MCP authorization server that supports JWT bearer grant.
func setupMCPAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	var server *httptest.Server

	// OAuth metadata endpoint - uses closure to get server URL
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                           server.URL,
			"token_endpoint":                   server.URL + "/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})

	// Token endpoint - supports JWT bearer grant (RFC 7523)
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}

		grantType := r.Form.Get("grant_type")
		if grantType != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			http.Error(w, fmt.Sprintf("unsupported grant_type: %s", grantType), http.StatusBadRequest)
			return
		}

		// Verify client authentication
		clientID := r.Form.Get("client_id")
		clientSecret := r.Form.Get("client_secret")
		if clientID != "mcp_client_id" || clientSecret != "mcp_secret" {
			http.Error(w, "invalid client credentials", http.StatusUnauthorized)
			return
		}

		// Verify assertion (ID-JAG)
		assertion := r.Form.Get("assertion")
		if assertion != "id-jag-token-from-idp" {
			http.Error(w, "invalid assertion", http.StatusBadRequest)
			return
		}

		// Return access token
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "mcp_access_token_from_jwt_bearer",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        "read write",
		})
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

// TestEnterpriseHandler_Authorize_IDTokenFetcherError tests error handling when IDTokenFetcher fails.
func TestEnterpriseHandler_Authorize_IDTokenFetcherError(t *testing.T) {
	handler, err := NewEnterpriseHandler(&EnterpriseHandlerConfig{
		IdPIssuerURL: "https://idp.example.com",
		IdPCredentials: &oauthex.ClientCredentials{
			ClientID: "idp_client_id",
		},
		MCPAuthServerURL: "https://mcp-auth.example.com",
		MCPResourceURI:   "https://mcp.example.com",
		MCPCredentials: &oauthex.ClientCredentials{
			ClientID: "mcp_client_id",
		},
		IDTokenFetcher: func(ctx context.Context) (*oauth2.Token, error) {
			return nil, fmt.Errorf("user cancelled login")
		},
	})
	if err != nil {
		t.Fatalf("NewEnterpriseHandler failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://mcp.example.com/api", nil)
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}

	err = handler.Authorize(context.Background(), req, resp)
	if err == nil {
		t.Fatal("expected error from Authorize, got nil")
	}
	if !strings.Contains(err.Error(), "failed to obtain ID token") {
		t.Errorf("expected error about ID token, got: %v", err)
	}
}

// TestEnterpriseHandler_TokenSource_BeforeAuthorization tests TokenSource before authorization.
func TestEnterpriseHandler_TokenSource_BeforeAuthorization(t *testing.T) {
	handler, err := NewEnterpriseHandler(&EnterpriseHandlerConfig{
		IdPIssuerURL: "https://idp.example.com",
		IdPCredentials: &oauthex.ClientCredentials{
			ClientID: "idp_client_id",
		},
		MCPAuthServerURL: "https://mcp-auth.example.com",
		MCPResourceURI:   "https://mcp.example.com",
		MCPCredentials: &oauthex.ClientCredentials{
			ClientID: "mcp_client_id",
		},
		IDTokenFetcher: func(ctx context.Context) (*oauth2.Token, error) {
			token := &oauth2.Token{
				AccessToken: "mock_access_token",
				TokenType:   "Bearer",
			}
			return token.WithExtra(map[string]interface{}{"id_token": "mock_id_token"}), nil
		},
	})
	if err != nil {
		t.Fatalf("NewEnterpriseHandler failed: %v", err)
	}

	tokenSource, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource failed: %v", err)
	}
	if tokenSource != nil {
		t.Errorf("expected nil token source before authorization, got %v", tokenSource)
	}
}
