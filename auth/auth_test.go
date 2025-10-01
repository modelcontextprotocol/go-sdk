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

func TestRequireBearerToken_ClaimsTable(t *testing.T) {
	issuedAt := time.Unix(1730000000, 0).UTC()
	notBefore := issuedAt.Add(-time.Minute)

	verifier := func(_ context.Context, token string, _ *http.Request) (*TokenInfo, error) {
		switch token {
		case "claims":
			return &TokenInfo{
				Scopes:     []string{"s1"},
				Expiration: time.Now().Add(time.Hour),
				Issuer:     "https://issuer.example",
				Subject:    "user-123",
				Audience:   []string{"aud1", "aud2"},
				NotBefore:  notBefore,
				IssuedAt:   issuedAt,
				JWTID:      "jwt-id-abc",
			}, nil
		case "claims-zero":
			return &TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
		default:
			return nil, ErrInvalidToken
		}
	}

	for _, tt := range []struct {
		name      string
		header    string
		checkFunc func(t *testing.T, ti *TokenInfo)
	}{
		{
			name:   "claims present",
			header: "Bearer claims",
			checkFunc: func(t *testing.T, ti *TokenInfo) {
				if ti == nil {
					t.Fatalf("TokenInfo missing in context")
				}
				if ti.Issuer != "https://issuer.example" {
					t.Fatalf("iss got %q", ti.Issuer)
				}
				if ti.Subject != "user-123" {
					t.Fatalf("sub got %q", ti.Subject)
				}
				if len(ti.Audience) != 2 || ti.Audience[0] != "aud1" || ti.Audience[1] != "aud2" {
					t.Fatalf("aud got %v", ti.Audience)
				}
				if ti.NotBefore.IsZero() {
					t.Fatalf("nbf is zero")
				}
				if ti.IssuedAt.IsZero() {
					t.Fatalf("iat is zero")
				}
				if ti.JWTID != "jwt-id-abc" {
					t.Fatalf("jti got %q", ti.JWTID)
				}
				if ti.Expiration.IsZero() {
					t.Fatalf("exp is zero")
				}
			},
		},
		{
			name:   "claims zero values (except exp)",
			header: "Bearer claims-zero",
			checkFunc: func(t *testing.T, ti *TokenInfo) {
				if ti == nil {
					t.Fatalf("TokenInfo missing in context")
				}
				if ti.Issuer != "" {
					t.Fatalf("iss expected empty, got %q", ti.Issuer)
				}
				if ti.Subject != "" {
					t.Fatalf("sub expected empty, got %q", ti.Subject)
				}
				if len(ti.Audience) != 0 {
					t.Fatalf("aud expected empty, got %v", ti.Audience)
				}
				if !ti.NotBefore.IsZero() {
					t.Fatalf("nbf expected zero, got %v", ti.NotBefore)
				}
				if !ti.IssuedAt.IsZero() {
					t.Fatalf("iat expected zero, got %v", ti.IssuedAt)
				}
				if ti.JWTID != "" {
					t.Fatalf("jti expected empty, got %q", ti.JWTID)
				}
				if ti.Expiration.IsZero() {
					t.Fatalf("exp should be set for middleware to pass")
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", tt.header)
			rw := httptest.NewRecorder()

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ti := TokenInfoFromContext(r.Context())
				// Run the provided check against the token info in context
				tt.checkFunc(t, ti)
				w.WriteHeader(http.StatusOK)
			})

			wrapped := RequireBearerToken(verifier, nil)(handler)
			wrapped.ServeHTTP(rw, req)
			if rw.Result().StatusCode != http.StatusOK {
				t.Fatalf("unexpected status: %d", rw.Result().StatusCode)
			}
		})
	}
}
