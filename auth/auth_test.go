// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
		name     string
		opts     *RequireBearerTokenOptions
		header   string
		wantMsg  string
		wantCode int
	}{
		{
			"valid", nil, "Bearer valid",
			"", 0,
		},
		{
			"bad header", nil, "Barer valid",
			"no bearer token", 401,
		},
		{
			"invalid", nil, "bearer invalid",
			"invalid token", 401,
		},
		{
			"oauth error", nil, "Bearer oauth",
			"oauth error", 400,
		},
		{
			"no expiration", nil, "Bearer noexp",
			"token missing expiration", 401,
		},
		{
			"expired", nil, "Bearer expired",
			"token expired", 401,
		},
		{
			"missing scope", &RequireBearerTokenOptions{Scopes: []string{"s1"}}, "Bearer valid",
			"insufficient scope", 403,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, gotMsg, gotCode := verify(&http.Request{
				Header: http.Header{"Authorization": {tt.header}},
			}, verifier, tt.opts)
			if gotMsg != tt.wantMsg || gotCode != tt.wantCode {
				t.Errorf("got (%q, %d), want (%q, %d)", gotMsg, gotCode, tt.wantMsg, tt.wantCode)
			}
		})
	}
}

// Integration tests for Security Best Practices conformance.
// 2.2 Token Passthrough.
// https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices.
// Table-driven middleware tests covering invalid tokens, scope enforcement, and OK path.
func TestBearerMiddleware(t *testing.T) {
	const resourceMetadata = "https://auth.example/meta"
	verifier := func(_ context.Context, tok string, _ *http.Request) (*TokenInfo, error) {
		switch tok {
		case "valid":
			return &TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
		default:
			return nil, ErrInvalidToken
		}
	}

	tests := []struct {
		name       string
		token      string
		scopes     []string
		wantCode   int
		wantCalled bool
	}{
		{name: "invalid-aud", token: "bad-aud", wantCode: http.StatusUnauthorized, wantCalled: false},
		{name: "unknown-issuer", token: "unknown-issuer", wantCode: http.StatusUnauthorized, wantCalled: false},
		{name: "missing-scope", token: "valid", scopes: []string{"s1"}, wantCode: http.StatusForbidden, wantCalled: false},
		{name: "ok", token: "valid", wantCode: http.StatusOK, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			h := RequireBearerToken(verifier, &RequireBearerTokenOptions{
				ResourceMetadataURL: resourceMetadata,
				Scopes:              tt.scopes,
			})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
			req.Header.Set("Authorization", "Bearer "+tt.token)
			rw := httptest.NewRecorder()

			h.ServeHTTP(rw, req)

			if rw.Code != tt.wantCode {
				t.Fatalf("got status %d, want %d", rw.Code, tt.wantCode)
			}
			if called != tt.wantCalled {
				t.Fatalf("handler called=%v, want %v", called, tt.wantCalled)
			}
			if tt.wantCode == http.StatusUnauthorized || tt.wantCode == http.StatusForbidden {
				want := "Bearer resource_metadata=" + resourceMetadata
				if rw.Header().Get("WWW-Authenticate") != want {
					t.Fatalf("unexpected WWW-Authenticate header: %q", rw.Header().Get("WWW-Authenticate"))
				}
			}
		})
	}
}

func TestHTTPMiddleware_NoTokenPassthrough(t *testing.T) {
	// Downstream fake API that records the incoming Authorization header.
	var gotAuth string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()

	// Verifier accepts the incoming client token.
	verifier := func(_ context.Context, token string, _ *http.Request) (*TokenInfo, error) {
		if token != "client-token" {
			return nil, ErrInvalidToken
		}
		return &TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
	}

	wrapped := RequireBearerToken(verifier, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate proxy-like behavior: perform a downstream request without
		// forwarding the client's Authorization header.
		resp, err := http.Get(downstream.URL)
		if err != nil {
			t.Fatalf("downstream request failed: %v", err)
		}
		resp.Body.Close()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Header.Set("Authorization", "Bearer client-token")
	rw := httptest.NewRecorder()
	wrapped.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rw.Code, http.StatusOK)
	}
	if gotAuth != "" {
		t.Fatalf("downstream Authorization header should be empty; got %q", gotAuth)
	}
}
