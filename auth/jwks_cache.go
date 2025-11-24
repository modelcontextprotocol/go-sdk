// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// This file implements JWKS (JSON Web Key Set) fetching and caching for
// JWT signature verification in Enterprise Managed Authorization (SEP-990).

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// JWK represents a JSON Web Key per RFC 7517.
type JWK struct {
	// KeyType is the key type (e.g., "RSA", "EC").
	KeyType string `json:"kty"`
	// Use indicates the intended use of the key (e.g., "sig" for signature).
	Use string `json:"use,omitempty"`
	// KeyID is the key identifier.
	KeyID string `json:"kid"`
	// Algorithm is the algorithm intended for use with the key.
	Algorithm string `json:"alg,omitempty"`
	// N is the RSA modulus (base64url encoded).
	N string `json:"n,omitempty"`
	// E is the RSA public exponent (base64url encoded).
	E string `json:"e,omitempty"`
	// X is the X coordinate for elliptic curve keys (base64url encoded).
	X string `json:"x,omitempty"`
	// Y is the Y coordinate for elliptic curve keys (base64url encoded).
	Y string `json:"y,omitempty"`
	// Curve is the elliptic curve name (e.g., "P-256").
	Curve string `json:"crv,omitempty"`
}

// JWKS represents a JSON Web Key Set per RFC 7517.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// FindKey finds a key by its key ID (kid).
func (j *JWKS) FindKey(kid string) (*JWK, error) {
	for i := range j.Keys {
		if j.Keys[i].KeyID == kid {
			return &j.Keys[i], nil
		}
	}
	return nil, fmt.Errorf("key with kid %q not found", kid)
}

// JWKSCache caches JWKS responses to reduce network requests.
type JWKSCache struct {
	mu      sync.RWMutex
	entries map[string]*jwksCacheEntry
	client  *http.Client
}
type jwksCacheEntry struct {
	jwks      *JWKS
	expiresAt time.Time
}

// NewJWKSCache creates a new JWKS cache with the given HTTP client.
// If client is nil, http.DefaultClient is used.
func NewJWKSCache(client *http.Client) *JWKSCache {
	if client == nil {
		client = http.DefaultClient
	}
	return &JWKSCache{
		entries: make(map[string]*jwksCacheEntry),
		client:  client,
	}
}

// Get fetches JWKS from the given URL, using cache if available and not expired.
// The cache duration is 1 hour per best practices for JWKS caching.
func (c *JWKSCache) Get(ctx context.Context, jwksURL string) (*JWKS, error) {
	// Check cache first
	c.mu.RLock()
	entry, ok := c.entries[jwksURL]
	c.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.jwks, nil
	}
	// Fetch from network
	jwks, err := c.fetch(ctx, jwksURL)
	if err != nil {
		return nil, err
	}
	// Update cache
	c.mu.Lock()
	c.entries[jwksURL] = &jwksCacheEntry{
		jwks:      jwks,
		expiresAt: time.Now().Add(1 * time.Hour),
	}
	c.mu.Unlock()
	return jwks, nil
}

// fetch retrieves JWKS from the given URL.
func (c *JWKSCache) fetch(ctx context.Context, jwksURL string) (*JWKS, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}
	// Read response body (limit to 1MB for safety)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read JWKS response: %w", err)
	}
	// Parse JWKS
	var jwks JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, fmt.Errorf("failed to parse JWKS: %w", err)
	}
	if len(jwks.Keys) == 0 {
		return nil, fmt.Errorf("JWKS contains no keys")
	}
	return &jwks, nil
}

// Invalidate removes a JWKS entry from the cache, forcing a fresh fetch on next Get.
func (c *JWKSCache) Invalidate(jwksURL string) {
	c.mu.Lock()
	delete(c.entries, jwksURL)
	c.mu.Unlock()
}

// Clear removes all entries from the cache.
func (c *JWKSCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]*jwksCacheEntry)
	c.mu.Unlock()
}
