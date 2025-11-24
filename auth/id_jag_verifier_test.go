// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestIDJAGVerifier tests ID-JAG validation.
func TestIDJAGVerifier(t *testing.T) {
	// Generate RSA key pair for testing
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	publicKey := &privateKey.PublicKey
	// Create mock JWKS server
	jwksServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwks := &JWKS{
			Keys: []JWK{
				{
					KeyType:   "RSA",
					Use:       "sig",
					KeyID:     "test-key",
					Algorithm: "RS256",
					N:         base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
					E:         base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()
	// Configure verifier
	config := &IDJAGVerifierConfig{
		AuthServerIssuerURL: "https://auth.mcpserver.example",
		TrustedIdPs: map[string]*TrustedIdPConfig{
			"test-idp": {
				IssuerURL: "https://test.okta.com",
				JWKSURL:   jwksServer.URL,
			},
		},
		HTTPClient: jwksServer.Client(),
	}
	verifier := NewIDJAGVerifier(config)
	// Test valid ID-JAG
	t.Run("valid ID-JAG", func(t *testing.T) {
		idJAG := createTestIDJAG(t, privateKey, map[string]interface{}{
			"iss":       "https://test.okta.com",
			"sub":       "user123",
			"aud":       "https://auth.mcpserver.example",
			"resource":  "https://mcp.mcpserver.example",
			"client_id": "client123",
			"jti":       "jti-" + fmt.Sprint(time.Now().UnixNano()),
			"exp":       time.Now().Add(1 * time.Hour).Unix(),
			"iat":       time.Now().Unix(),
			"scope":     "read write",
		})
		tokenInfo, err := verifier(context.Background(), idJAG, nil)
		if err != nil {
			t.Fatalf("Verify failed: %v", err)
		}
		if len(tokenInfo.Scopes) != 2 {
			t.Errorf("expected 2 scopes, got %d", len(tokenInfo.Scopes))
		}
		if tokenInfo.Extra["sub"] != "user123" {
			t.Errorf("expected sub 'user123', got %v", tokenInfo.Extra["sub"])
		}
		if tokenInfo.Extra["client_id"] != "client123" {
			t.Errorf("expected client_id 'client123', got %v", tokenInfo.Extra["client_id"])
		}
	})
	// Test expired ID-JAG
	t.Run("expired ID-JAG", func(t *testing.T) {
		idJAG := createTestIDJAG(t, privateKey, map[string]interface{}{
			"iss":       "https://test.okta.com",
			"sub":       "user123",
			"aud":       "https://auth.mcpserver.example",
			"resource":  "https://mcp.mcpserver.example",
			"client_id": "client123",
			"jti":       "jti-expired",
			"exp":       time.Now().Add(-1 * time.Hour).Unix(),
			"iat":       time.Now().Add(-2 * time.Hour).Unix(),
			"scope":     "read write",
		})
		_, err := verifier(context.Background(), idJAG, nil)
		if err == nil {
			t.Error("expected error for expired ID-JAG, got nil")
		}
	})
	// Test wrong audience
	t.Run("wrong audience", func(t *testing.T) {
		idJAG := createTestIDJAG(t, privateKey, map[string]interface{}{
			"iss":       "https://test.okta.com",
			"sub":       "user123",
			"aud":       "https://wrong.audience.com",
			"resource":  "https://mcp.mcpserver.example",
			"client_id": "client123",
			"jti":       "jti-wrong-aud",
			"exp":       time.Now().Add(1 * time.Hour).Unix(),
			"iat":       time.Now().Unix(),
			"scope":     "read write",
		})
		_, err := verifier(context.Background(), idJAG, nil)
		if err == nil {
			t.Error("expected error for wrong audience, got nil")
		}
		if !strings.Contains(err.Error(), "invalid audience") {
			t.Errorf("expected 'invalid audience' error, got: %v", err)
		}
	})
	// Test untrusted issuer
	t.Run("untrusted issuer", func(t *testing.T) {
		idJAG := createTestIDJAG(t, privateKey, map[string]interface{}{
			"iss":       "https://untrusted.idp.com",
			"sub":       "user123",
			"aud":       "https://auth.mcpserver.example",
			"resource":  "https://mcp.mcpserver.example",
			"client_id": "client123",
			"jti":       "jti-untrusted",
			"exp":       time.Now().Add(1 * time.Hour).Unix(),
			"iat":       time.Now().Unix(),
			"scope":     "read write",
		})
		_, err := verifier(context.Background(), idJAG, nil)
		if err == nil {
			t.Error("expected error for untrusted issuer, got nil")
		}
		if !strings.Contains(err.Error(), "untrusted issuer") {
			t.Errorf("expected 'untrusted issuer' error, got: %v", err)
		}
	})
	// Test replay attack
	t.Run("replay attack", func(t *testing.T) {
		jti := "jti-replay-" + fmt.Sprint(time.Now().UnixNano())
		idJAG := createTestIDJAG(t, privateKey, map[string]interface{}{
			"iss":       "https://test.okta.com",
			"sub":       "user123",
			"aud":       "https://auth.mcpserver.example",
			"resource":  "https://mcp.mcpserver.example",
			"client_id": "client123",
			"jti":       jti,
			"exp":       time.Now().Add(1 * time.Hour).Unix(),
			"iat":       time.Now().Unix(),
			"scope":     "read write",
		})
		// First use should succeed
		_, err := verifier(context.Background(), idJAG, nil)
		if err != nil {
			t.Fatalf("First verify failed: %v", err)
		}
		// Second use (replay) should fail
		_, err = verifier(context.Background(), idJAG, nil)
		if err == nil {
			t.Error("expected error for replay attack, got nil")
		}
		if !strings.Contains(err.Error(), "already used") {
			t.Errorf("expected 'already used' error, got: %v", err)
		}
	})
}

// createTestIDJAG creates a test ID-JAG JWT signed with the given private key.
func createTestIDJAG(t *testing.T, privateKey *rsa.PrivateKey, claims map[string]interface{}) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))
	token.Header["typ"] = "oauth-id-jag+jwt"
	token.Header["kid"] = "test-key"
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("Failed to sign token: %v", err)
	}
	return signedToken
}
