// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

func TestVerify(t *testing.T) {
	verifier := func(_ context.Context, token string, _ *http.Request) (*TokenInfo, error) {
		switch token {
		case "valid":
			return &TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
		case "invalid":
			return nil, ErrInvalidToken
		case "oauth":
			return nil, ErrOAuth
		case "noexp":
			return &TokenInfo{}, nil
		case "expired":
			return &TokenInfo{Expiration: time.Now().Add(-time.Hour)}, nil
		default:
			return nil, errors.New("unknown")
		}
	}

	for _, tt := range []struct {
		name          string
		opts          *RequireBearerTokenOptions
		header        string
		wantMsg       string
		wantCode      int
		wantAuthError string // RFC 6750 error code to advertise, "" if none
	}{
		{
			"valid", nil, "Bearer valid",
			"", 0, "",
		},
		{
			"bad header", nil, "Barer valid",
			"no bearer token", 401, "",
		},
		{
			"invalid", nil, "bearer invalid",
			"invalid token", 401, "invalid_token",
		},
		{
			"oauth error", nil, "Bearer oauth",
			"oauth error", 400, "invalid_request",
		},
		{
			"no expiration", nil, "Bearer noexp",
			"token missing expiration", 401, "invalid_token",
		},
		{
			"no expiration with AllowMissingExpiration accepts",
			&RequireBearerTokenOptions{AllowMissingExpiration: true}, "Bearer noexp",
			"", 0, "",
		},
		{
			"expired", nil, "Bearer expired",
			"token expired", 401, "invalid_token",
		},
		{
			"missing scope", &RequireBearerTokenOptions{Scopes: []string{"s1"}}, "Bearer valid",
			"insufficient scope", 403, "insufficient_scope",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, gotMsg, gotCode, gotAuthError := verify(&http.Request{
				Header: http.Header{"Authorization": {tt.header}},
			}, verifier, tt.opts)
			if gotMsg != tt.wantMsg || gotCode != tt.wantCode || gotAuthError != tt.wantAuthError {
				t.Errorf("got (%q, %d, %q), want (%q, %d, %q)",
					gotMsg, gotCode, gotAuthError, tt.wantMsg, tt.wantCode, tt.wantAuthError)
			}
		})
	}
}

func TestRequireBearerTokenAdvertisesInsufficientScope(t *testing.T) {
	// A valid token lacking a required scope must yield a 403 whose
	// WWW-Authenticate challenge carries error="insufficient_scope". The SDK's
	// own client step-up flow (AuthorizationCodeHandler.Authorize) re-authorizes
	// only when it sees exactly that value, so without it step-up never fires.
	verifier := func(_ context.Context, _ string, _ *http.Request) (*TokenInfo, error) {
		return &TokenInfo{Expiration: time.Now().Add(time.Hour), Scopes: []string{"read"}}, nil
	}
	mw := RequireBearerToken(verifier, &RequireBearerTokenOptions{
		Scopes:              []string{"admin"},
		ResourceMetadataURL: "https://example.com/.well-known/oauth-protected-resource",
	})
	handler := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler should not be reached on insufficient scope")
	}))

	req := httptest.NewRequest(http.MethodGet, "https://example.com/mcp", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	// Parse the challenge exactly as the client does (oauthex.ParseWWWAuthenticate
	// + a "bearer" scheme match) and confirm the error code is visible.
	challenges, err := oauthex.ParseWWWAuthenticate(rec.Result().Header[http.CanonicalHeaderKey("WWW-Authenticate")])
	if err != nil {
		t.Fatalf("ParseWWWAuthenticate: %v", err)
	}
	var gotError string
	for _, c := range challenges {
		if c.Scheme == "bearer" && c.Params["error"] != "" {
			gotError = c.Params["error"]
		}
	}
	if gotError != "insufficient_scope" {
		t.Errorf("WWW-Authenticate error param = %q, want %q (header: %q)",
			gotError, "insufficient_scope", rec.Result().Header.Get("WWW-Authenticate"))
	}
}

func TestProtectedResourceMetadataHandler(t *testing.T) {
	metadata := &oauthex.ProtectedResourceMetadata{
		Resource: "https://example.com/mcp",
		AuthorizationServers: []string{
			"https://auth.example.com/.well-known/openid-configuration",
		},
		ScopesSupported: []string{"read", "write"},
	}

	handler := ProtectedResourceMetadataHandler(metadata)

	tests := []struct {
		name       string
		method     string
		wantStatus int
		checkJSON  bool
	}{
		{
			name:       "GET returns metadata",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			checkJSON:  true,
		},
		{
			name:       "OPTIONS for CORS preflight",
			method:     http.MethodOptions,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "POST not allowed",
			method:     http.MethodPost,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "PUT not allowed",
			method:     http.MethodPut,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "DELETE not allowed",
			method:     http.MethodDelete,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/.well-known/oauth-protected-resource", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			// All responses should have CORS headers
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
			}

			if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
				t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, "GET, OPTIONS")
			}

			// Validate error response body for disallowed methods
			if tt.wantStatus == http.StatusMethodNotAllowed {
				if !strings.Contains(rec.Body.String(), "Method not allowed") {
					t.Errorf("error body = %q, want to contain %q", rec.Body.String(), "Method not allowed")
				}
			}

			if tt.checkJSON {
				if got := rec.Header().Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want %q", got, "application/json")
				}

				var got oauthex.ProtectedResourceMetadata
				if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
					t.Fatalf("failed to decode response: %v", err)
				}

				if got.Resource != metadata.Resource {
					t.Errorf("Resource = %q, want %q", got.Resource, metadata.Resource)
				}

				if len(got.AuthorizationServers) != len(metadata.AuthorizationServers) {
					t.Errorf("AuthorizationServers length = %d, want %d",
						len(got.AuthorizationServers), len(metadata.AuthorizationServers))
				}

				for i, server := range got.AuthorizationServers {
					if server != metadata.AuthorizationServers[i] {
						t.Errorf("AuthorizationServers[%d] = %q, want %q",
							i, server, metadata.AuthorizationServers[i])
					}
				}

				if len(got.ScopesSupported) != len(metadata.ScopesSupported) {
					t.Errorf("ScopesSupported length = %d, want %d",
						len(got.ScopesSupported), len(metadata.ScopesSupported))
				}
			}
		})
	}
}

func TestRequireBearerToken(t *testing.T) {
	verifier := func(_ context.Context, token string, _ *http.Request) (*TokenInfo, error) {
		if token == "valid" {
			return &TokenInfo{Expiration: time.Now().Add(time.Hour), Scopes: []string{"read"}}, nil
		}
		return nil, ErrInvalidToken
	}

	tests := []struct {
		name       string
		opts       *RequireBearerTokenOptions
		authHeader string
		wantHeader string
		wantStatus int
	}{
		{
			name:       "no middleware options",
			opts:       nil,
			authHeader: "Bearer invalid",
			wantHeader: "Bearer error=\"invalid_token\"",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "metadata only",
			opts: &RequireBearerTokenOptions{
				ResourceMetadataURL: "https://example.com/resource-metadata",
			},
			authHeader: "Bearer invalid",
			wantHeader: "Bearer error=\"invalid_token\", resource_metadata=\"https://example.com/resource-metadata\"",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "scopes only",
			opts: &RequireBearerTokenOptions{
				Scopes: []string{"read", "write"},
			},
			authHeader: "Bearer invalid",
			wantHeader: "Bearer error=\"invalid_token\", scope=\"read write\"",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "metadata and scopes",
			opts: &RequireBearerTokenOptions{
				ResourceMetadataURL: "https://example.com/resource-metadata",
				Scopes:              []string{"read", "write"},
			},
			authHeader: "Bearer invalid",
			wantHeader: "Bearer error=\"invalid_token\", resource_metadata=\"https://example.com/resource-metadata\", scope=\"read write\"",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "insufficient scope",
			opts: &RequireBearerTokenOptions{
				Scopes: []string{"admin"},
			},
			authHeader: "Bearer valid", // Has "read", needs "admin" -> 403
			wantHeader: "Bearer error=\"insufficient_scope\", scope=\"admin\"",
			wantStatus: http.StatusForbidden,
		},
		{
			name: "success",
			opts: &RequireBearerTokenOptions{
				Scopes: []string{"read"},
			},
			authHeader: "Bearer valid",
			wantHeader: "",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RequireBearerToken(verifier, tt.opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			got := rec.Header().Get("WWW-Authenticate")
			if got != tt.wantHeader {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

// TestRequireBearerToken_ClockSkew verifies that the ClockSkew option
// extends the expiration check tolerance: a token whose Expiration is in the
// recent past is accepted iff the elapsed interval is within ClockSkew.
func TestRequireBearerToken_ClockSkew(t *testing.T) {
	tests := []struct {
		name       string
		clockSkew  time.Duration
		expiredAgo time.Duration
		wantStatus int
	}{
		{
			name:       "no skew, fresh token accepted",
			clockSkew:  0,
			expiredAgo: -time.Minute, // expires in 1 minute
			wantStatus: http.StatusOK,
		},
		{
			name:       "no skew, expired token rejected",
			clockSkew:  0,
			expiredAgo: 5 * time.Second, // expired 5s ago
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "with skew, recently-expired token accepted",
			clockSkew:  30 * time.Second,
			expiredAgo: 5 * time.Second,
			wantStatus: http.StatusOK,
		},
		{
			name:       "with skew, token expired beyond tolerance rejected",
			clockSkew:  10 * time.Second,
			expiredAgo: 30 * time.Second,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := func(_ context.Context, _ string, _ *http.Request) (*TokenInfo, error) {
				return &TokenInfo{Expiration: time.Now().Add(-tt.expiredAgo)}, nil
			}
			handler := RequireBearerToken(verifier, &RequireBearerTokenOptions{
				ClockSkew: tt.clockSkew,
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer anything")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}
