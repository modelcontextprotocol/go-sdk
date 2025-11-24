// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements ID-JAG (Identity Assertion JWT Authorization Grant) validation
// for MCP Servers in Enterprise Managed Authorization (SEP-990).

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// TrustedIdPConfig contains configuration for a trusted Identity Provider.
type TrustedIdPConfig struct {
	// IssuerURL is the IdP's issuer URL (must match the iss claim).
	IssuerURL string
	// JWKSURL is the URL to fetch the IdP's JSON Web Key Set.
	JWKSURL string
}

// IDJAGVerifierConfig configures ID-JAG validation for an MCP Server.
type IDJAGVerifierConfig struct {
	// AuthServerIssuerURL is this MCP Server's authorization server issuer URL.
	// This must match the aud claim in the ID-JAG.
	AuthServerIssuerURL string
	// TrustedIdPs is a map of trusted Identity Providers.
	// The key is a friendly name, the value is the IdP configuration.
	TrustedIdPs map[string]*TrustedIdPConfig
	// JWKSCache is the cache for JWKS responses. If nil, a new cache is created.
	JWKSCache *JWKSCache
	// HTTPClient is the HTTP client for fetching JWKS. If nil, http.DefaultClient is used.
	HTTPClient *http.Client
	// AllowedClockSkew is the allowed clock skew for exp/iat validation.
	// Default is 5 minutes.
	AllowedClockSkew time.Duration
}

// IDJAGVerifier validates ID-JAG tokens for MCP Servers.
type IDJAGVerifier struct {
	config    *IDJAGVerifierConfig
	jwksCache *JWKSCache
	usedJTIs  map[string]time.Time // Replay attack prevention
	usedJTIMu sync.RWMutex
}

// NewIDJAGVerifier creates a new ID-JAG verifier with the given configuration.
// This returns a TokenVerifier that can be used with RequireBearerToken middleware.
//
// Example:
//
//	config := &IDJAGVerifierConfig{
//		AuthServerIssuerURL: "https://auth.mcpserver.example",
//		TrustedIdPs: map[string]*TrustedIdPConfig{
//			"acme-okta": {
//				IssuerURL: "https://acme.okta.com",
//				JWKSURL:   "https://acme.okta.com/.well-known/jwks.json",
//			},
//		},
//	}
//
//	verifier := NewIDJAGVerifier(config)
//	middleware := RequireBearerToken(verifier, &RequireBearerTokenOptions{
//		Scopes: []string{"read"},
//	})
func NewIDJAGVerifier(config *IDJAGVerifierConfig) TokenVerifier {
	if config.JWKSCache == nil {
		config.JWKSCache = NewJWKSCache(config.HTTPClient)
	}
	if config.AllowedClockSkew == 0 {
		config.AllowedClockSkew = 5 * time.Minute
	}
	verifier := &IDJAGVerifier{
		config:    config,
		jwksCache: config.JWKSCache,
		usedJTIs:  make(map[string]time.Time),
	}
	// Start cleanup goroutine for JTI tracking
	go verifier.cleanupExpiredJTIs()
	return verifier.Verify
}

// Verify validates an ID-JAG token and returns TokenInfo.
// This implements the TokenVerifier interface.
func (v *IDJAGVerifier) Verify(ctx context.Context, token string, req *http.Request) (*TokenInfo, error) {
	// Step 1: Parse the ID-JAG (without signature verification yet)
	claims, err := oauthex.ParseIDJAG(token)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse ID-JAG: %v", ErrInvalidToken, err)
	}
	// Step 2: Check if expired (with clock skew)
	expiryTime := time.Unix(claims.ExpiresAt, 0)
	if time.Now().After(expiryTime.Add(v.config.AllowedClockSkew)) {
		return nil, fmt.Errorf("%w: ID-JAG expired at %v", ErrInvalidToken, expiryTime)
	}
	// Step 3: Validate aud claim per SEP-990 Section 5.1
	if claims.Audience != v.config.AuthServerIssuerURL {
		return nil, fmt.Errorf("%w: invalid audience: expected %q, got %q",
			ErrInvalidToken, v.config.AuthServerIssuerURL, claims.Audience)
	}
	// Step 4: Find trusted IdP
	var trustedIdP *TrustedIdPConfig
	for _, idp := range v.config.TrustedIdPs {
		if idp.IssuerURL == claims.Issuer {
			trustedIdP = idp
			break
		}
	}
	if trustedIdP == nil {
		return nil, fmt.Errorf("%w: untrusted issuer: %q", ErrInvalidToken, claims.Issuer)
	}
	// Step 5: Verify JWT signature using IdP's JWKS
	if err := v.verifySignature(ctx, token, trustedIdP.JWKSURL); err != nil {
		return nil, fmt.Errorf("%w: signature verification failed: %v", ErrInvalidToken, err)
	}
	// Step 6: Replay attack prevention (check JTI)
	if err := v.checkJTI(claims.JTI, expiryTime); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	// Step 7: Return TokenInfo
	scopes := []string{}
	if claims.Scope != "" {
		scopes = strings.Split(claims.Scope, " ")
	}
	return &TokenInfo{
		Scopes:     scopes,
		Expiration: expiryTime,
		Extra: map[string]any{
			"sub":       claims.Subject,
			"client_id": claims.ClientID,
			"resource":  claims.Resource,
			"iss":       claims.Issuer,
		},
	}, nil
}

// verifySignature verifies the JWT signature using the IdP's JWKS.
func (v *IDJAGVerifier) verifySignature(ctx context.Context, tokenString, jwksURL string) error {
	// Parse JWT to get header
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid JWT format")
	}
	// Decode header to get kid
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("failed to decode JWT header: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("failed to parse JWT header: %w", err)
	}
	// Fetch JWKS
	jwks, err := v.jwksCache.Get(ctx, jwksURL)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	// Find the key
	jwk, err := jwks.FindKey(header.Kid)
	if err != nil {
		return fmt.Errorf("key not found in JWKS: %w", err)
	}
	// Parse JWT with verification
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Verify algorithm
		if token.Method.Alg() != header.Alg {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// Convert JWK to public key
		return jwkToPublicKey(jwk)
	})
	if err != nil {
		return fmt.Errorf("JWT verification failed: %w", err)
	}
	if !token.Valid {
		return fmt.Errorf("JWT is invalid")
	}
	return nil
}

// checkJTI checks if the JTI has been used before (replay attack prevention).
func (v *IDJAGVerifier) checkJTI(jti string, expiresAt time.Time) error {
	v.usedJTIMu.Lock()
	defer v.usedJTIMu.Unlock()
	if _, used := v.usedJTIs[jti]; used {
		return fmt.Errorf("JTI %q already used (replay attack)", jti)
	}
	// Mark as used
	v.usedJTIs[jti] = expiresAt
	return nil
}

// cleanupExpiredJTIs periodically removes expired JTIs from the tracking map.
func (v *IDJAGVerifier) cleanupExpiredJTIs() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		v.usedJTIMu.Lock()
		now := time.Now()
		for jti, expiresAt := range v.usedJTIs {
			if now.After(expiresAt) {
				delete(v.usedJTIs, jti)
			}
		}
		v.usedJTIMu.Unlock()
	}
}

// jwkToPublicKey converts a JWK to a public key for signature verification.
func jwkToPublicKey(jwk *JWK) (interface{}, error) {
	switch jwk.KeyType {
	case "RSA":
		// Decode modulus
		nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
		if err != nil {
			return nil, fmt.Errorf("failed to decode modulus: %w", err)
		}
		// Decode exponent
		eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil {
			return nil, fmt.Errorf("failed to decode exponent: %w", err)
		}
		// Convert to big.Int
		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)
		return &rsa.PublicKey{
			N: n,
			E: int(e.Int64()),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported key type: %s", jwk.KeyType)
	}
}
