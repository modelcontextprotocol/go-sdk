// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements ID-JAG (Identity Assertion JWT Authorization Grant) parsing
// for Enterprise Managed Authorization (SEP-990).
// See https://github.com/modelcontextprotocol/ext-auth/blob/main/specification/draft/enterprise-managed-authorization.mdx

//go:build mcp_go_client_oauth

package oauthex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// IDJAGClaims represents the claims in an Identity Assertion JWT Authorization Grant
// per SEP-990 Section 4.3. The ID-JAG is issued by the IdP during token exchange
// and describes the authorization grant for accessing an MCP Server.
type IDJAGClaims struct {
	// Issuer is the IdP's issuer URL.
	Issuer string `json:"iss"`
	// Subject is the user identifier at the MCP Server.
	Subject string `json:"sub"`
	// Audience is the Issuer URL of the MCP Server's authorization server.
	Audience string `json:"aud"`
	// Resource is the Resource Identifier of the MCP Server.
	Resource string `json:"resource"`
	// ClientID is the identifier of the MCP Client that this JWT was issued to.
	ClientID string `json:"client_id"`
	// JTI is the unique identifier of this JWT.
	JTI string `json:"jti"`
	// ExpiresAt is the expiration time of this JWT (Unix timestamp).
	ExpiresAt int64 `json:"exp"`
	// IssuedAt is the time this JWT was issued (Unix timestamp).
	IssuedAt int64 `json:"iat"`
	// Scope is a space-separated list of scopes associated with the token.
	Scope string `json:"scope,omitempty"`
}

// Expiry returns the expiration time as a time.Time.
func (c *IDJAGClaims) Expiry() time.Time {
	return time.Unix(c.ExpiresAt, 0)
}

// IssuedTime returns the issued-at time as a time.Time.
func (c *IDJAGClaims) IssuedTime() time.Time {
	return time.Unix(c.IssuedAt, 0)
}

// IsExpired checks if the ID-JAG has expired.
func (c *IDJAGClaims) IsExpired() bool {
	return time.Now().After(c.Expiry())
}

// ParseIDJAG parses an ID-JAG JWT and extracts its claims without validating
// the signature. This is useful for inspecting the contents of an ID-JAG during
// development or debugging.
//
// For production use on the server-side, use ValidateIDJAG instead, which
// performs full signature validation and claim verification.
//
// The JWT must have a "typ" header of "oauth-id-jag+jwt" per SEP-990 Section 4.3.
//
// Example:
//
//	claims, err := ParseIDJAG(idJAG)
//	if err != nil {
//		log.Fatalf("Failed to parse ID-JAG: %v", err)
//	}
//	fmt.Printf("Subject: %s\n", claims.Subject)
//	fmt.Printf("Expires: %v\n", claims.Expiry())
func ParseIDJAG(jwt string) (*IDJAGClaims, error) {
	if jwt == "" {
		return nil, fmt.Errorf("JWT is empty")
	}
	// Split JWT into parts (header.payload.signature)
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}
	// Decode header to check typ claim
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT header: %w", err)
	}
	var header struct {
		Type string `json:"typ"`
		Alg  string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("failed to parse JWT header: %w", err)
	}
	// Verify typ claim per SEP-990 Section 4.3
	if header.Type != "oauth-id-jag+jwt" {
		return nil, fmt.Errorf("invalid JWT type: expected 'oauth-id-jag+jwt', got '%s'", header.Type)
	}
	// Decode payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}
	// Parse claims
	var claims IDJAGClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse JWT claims: %w", err)
	}
	// Validate required claims are present per SEP-990 Section 4.3
	if claims.Issuer == "" {
		return nil, fmt.Errorf("missing required claim: iss")
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("missing required claim: sub")
	}
	if claims.Audience == "" {
		return nil, fmt.Errorf("missing required claim: aud")
	}
	if claims.Resource == "" {
		return nil, fmt.Errorf("missing required claim: resource")
	}
	if claims.ClientID == "" {
		return nil, fmt.Errorf("missing required claim: client_id")
	}
	if claims.JTI == "" {
		return nil, fmt.Errorf("missing required claim: jti")
	}
	if claims.ExpiresAt == 0 {
		return nil, fmt.Errorf("missing required claim: exp")
	}
	if claims.IssuedAt == 0 {
		return nil, fmt.Errorf("missing required claim: iat")
	}
	return &claims, nil
}
