// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package oauthex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestParseIDJAG tests parsing of ID-JAG tokens.
func TestParseIDJAG(t *testing.T) {
	// Create a test ID-JAG JWT
	now := time.Now().Unix()

	header := map[string]string{
		"typ": "oauth-id-jag+jwt",
		"alg": "RS256",
	}

	claims := map[string]interface{}{
		"iss":       "https://acme.okta.com",
		"sub":       "alice@acme.com",
		"aud":       "https://auth.mcpserver.example",
		"resource":  "https://mcp.mcpserver.example",
		"client_id": "xyz789",
		"jti":       "unique-id-123",
		"exp":       now + 300,
		"iat":       now,
		"scope":     "read write",
	}
	// Encode header and payload
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	// Create fake JWT (header.payload.signature)
	fakeJWT := fmt.Sprintf("%s.%s.fake-signature", headerB64, claimsB64)
	// Test successful parsing
	t.Run("successful parse", func(t *testing.T) {
		parsed, err := ParseIDJAG(fakeJWT)
		if err != nil {
			t.Fatalf("ParseIDJAG failed: %v", err)
		}
		if parsed.Issuer != "https://acme.okta.com" {
			t.Errorf("expected issuer 'https://acme.okta.com', got '%s'", parsed.Issuer)
		}
		if parsed.Subject != "alice@acme.com" {
			t.Errorf("expected subject 'alice@acme.com', got '%s'", parsed.Subject)
		}
		if parsed.Audience != "https://auth.mcpserver.example" {
			t.Errorf("expected audience 'https://auth.mcpserver.example', got '%s'", parsed.Audience)
		}
		if parsed.Resource != "https://mcp.mcpserver.example" {
			t.Errorf("expected resource 'https://mcp.mcpserver.example', got '%s'", parsed.Resource)
		}
		if parsed.ClientID != "xyz789" {
			t.Errorf("expected client_id 'xyz789', got '%s'", parsed.ClientID)
		}
		if parsed.JTI != "unique-id-123" {
			t.Errorf("expected jti 'unique-id-123', got '%s'", parsed.JTI)
		}
		if parsed.Scope != "read write" {
			t.Errorf("expected scope 'read write', got '%s'", parsed.Scope)
		}
		if parsed.IsExpired() {
			t.Error("expected ID-JAG not to be expired")
		}
	})
	// Test empty JWT
	t.Run("empty JWT", func(t *testing.T) {
		_, err := ParseIDJAG("")
		if err == nil {
			t.Error("expected error for empty JWT, got nil")
		}
	})
	// Test invalid format
	t.Run("invalid format", func(t *testing.T) {
		_, err := ParseIDJAG("invalid.jwt")
		if err == nil {
			t.Error("expected error for invalid JWT format, got nil")
		}
	})
	// Test wrong typ header
	t.Run("wrong typ header", func(t *testing.T) {
		wrongHeader := map[string]string{
			"typ": "JWT", // Should be "oauth-id-jag+jwt"
			"alg": "RS256",
		}
		wrongHeaderJSON, _ := json.Marshal(wrongHeader)
		wrongHeaderB64 := base64.RawURLEncoding.EncodeToString(wrongHeaderJSON)
		wrongJWT := fmt.Sprintf("%s.%s.fake-signature", wrongHeaderB64, claimsB64)
		_, err := ParseIDJAG(wrongJWT)
		if err == nil {
			t.Error("expected error for wrong typ header, got nil")
		}
		if err != nil && !strings.Contains(err.Error(), "invalid JWT type") {
			t.Errorf("expected 'invalid JWT type' error, got: %v", err)
		}
	})
	// Test missing required claims
	t.Run("missing required claims", func(t *testing.T) {
		incompleteClaims := map[string]interface{}{
			"iss": "https://acme.okta.com",
			// Missing other required claims
		}
		incompleteJSON, _ := json.Marshal(incompleteClaims)
		incompleteB64 := base64.RawURLEncoding.EncodeToString(incompleteJSON)
		incompleteJWT := fmt.Sprintf("%s.%s.fake-signature", headerB64, incompleteB64)
		_, err := ParseIDJAG(incompleteJWT)
		if err == nil {
			t.Error("expected error for missing claims, got nil")
		}
	})
	// Test expired ID-JAG
	t.Run("expired ID-JAG", func(t *testing.T) {
		expiredClaims := map[string]interface{}{
			"iss":       "https://acme.okta.com",
			"sub":       "alice@acme.com",
			"aud":       "https://auth.mcpserver.example",
			"resource":  "https://mcp.mcpserver.example",
			"client_id": "xyz789",
			"jti":       "unique-id-123",
			"exp":       now - 300, // Expired 5 minutes ago
			"iat":       now - 600,
			"scope":     "read write",
		}
		expiredJSON, _ := json.Marshal(expiredClaims)
		expiredB64 := base64.RawURLEncoding.EncodeToString(expiredJSON)
		expiredJWT := fmt.Sprintf("%s.%s.fake-signature", headerB64, expiredB64)
		parsed, err := ParseIDJAG(expiredJWT)
		if err != nil {
			t.Fatalf("ParseIDJAG failed: %v", err)
		}
		if !parsed.IsExpired() {
			t.Error("expected ID-JAG to be expired")
		}
	})
}

// TestIDJAGClaimsMethods tests the helper methods on IDJAGClaims.
func TestIDJAGClaimsMethods(t *testing.T) {
	now := time.Now()
	claims := &IDJAGClaims{
		ExpiresAt: now.Add(1 * time.Hour).Unix(),
		IssuedAt:  now.Unix(),
	}
	// Test Expiry
	expiry := claims.Expiry()
	if expiry.Before(now) {
		t.Error("expected expiry to be in the future")
	}
	// Test IssuedTime
	issued := claims.IssuedTime()
	if issued.After(now.Add(1 * time.Second)) {
		t.Error("expected issued time to be in the past")
	}
	// Test IsExpired (should not be expired)
	if claims.IsExpired() {
		t.Error("expected claims not to be expired")
	}
	// Test IsExpired (should be expired)
	claims.ExpiresAt = now.Add(-1 * time.Hour).Unix()
	if !claims.IsExpired() {
		t.Error("expected claims to be expired")
	}
}
