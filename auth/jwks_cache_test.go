// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestJWKSCache tests JWKS fetching and caching.
func TestJWKSCache(t *testing.T) {
	// Create test JWKS
	testJWKS := &JWKS{
		Keys: []JWK{
			{
				KeyType:   "RSA",
				Use:       "sig",
				KeyID:     "test-key-1",
				Algorithm: "RS256",
				N:         "test-modulus",
				E:         "AQAB",
			},
			{
				KeyType:   "RSA",
				Use:       "sig",
				KeyID:     "test-key-2",
				Algorithm: "RS256",
				N:         "test-modulus-2",
				E:         "AQAB",
			},
		},
	}
	// Create test server
	var requestCount int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testJWKS)
	}))
	defer server.Close()
	cache := NewJWKSCache(server.Client())
	// Test first fetch
	t.Run("first fetch", func(t *testing.T) {
		jwks, err := cache.Get(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if len(jwks.Keys) != 2 {
			t.Errorf("expected 2 keys, got %d", len(jwks.Keys))
		}
		if jwks.Keys[0].KeyID != "test-key-1" {
			t.Errorf("expected key ID 'test-key-1', got '%s'", jwks.Keys[0].KeyID)
		}
		if requestCount != 1 {
			t.Errorf("expected 1 request, got %d", requestCount)
		}
	})
	// Test cache hit
	t.Run("cache hit", func(t *testing.T) {
		jwks, err := cache.Get(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if len(jwks.Keys) != 2 {
			t.Errorf("expected 2 keys from cache, got %d", len(jwks.Keys))
		}
		// Should still be 1 request (served from cache)
		if requestCount != 1 {
			t.Errorf("expected 1 request (cached), got %d", requestCount)
		}
	})
	// Test FindKey
	t.Run("find key", func(t *testing.T) {
		jwks, _ := cache.Get(context.Background(), server.URL)
		key, err := jwks.FindKey("test-key-2")
		if err != nil {
			t.Fatalf("FindKey failed: %v", err)
		}
		if key.KeyID != "test-key-2" {
			t.Errorf("expected key ID 'test-key-2', got '%s'", key.KeyID)
		}
		if key.N != "test-modulus-2" {
			t.Errorf("expected modulus 'test-modulus-2', got '%s'", key.N)
		}
	})
	// Test key not found
	t.Run("key not found", func(t *testing.T) {
		jwks, _ := cache.Get(context.Background(), server.URL)
		_, err := jwks.FindKey("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent key, got nil")
		}
	})
	// Test Invalidate
	t.Run("invalidate", func(t *testing.T) {
		cache.Invalidate(server.URL)
		// Next fetch should hit the server again
		_, err := cache.Get(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("Get after invalidate failed: %v", err)
		}
		if requestCount != 2 {
			t.Errorf("expected 2 requests after invalidate, got %d", requestCount)
		}
	})
	// Test Clear
	t.Run("clear", func(t *testing.T) {
		cache.Clear()
		// Next fetch should hit the server again
		_, err := cache.Get(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("Get after clear failed: %v", err)
		}
		if requestCount != 3 {
			t.Errorf("expected 3 requests after clear, got %d", requestCount)
		}
	})
	// Test error handling
	t.Run("server error", func(t *testing.T) {
		errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer errorServer.Close()
		_, err := cache.Get(context.Background(), errorServer.URL)
		if err == nil {
			t.Error("expected error for server error, got nil")
		}
	})
	// Test invalid JSON
	t.Run("invalid json", func(t *testing.T) {
		invalidServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("invalid json"))
		}))
		defer invalidServer.Close()
		_, err := cache.Get(context.Background(), invalidServer.URL)
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})
	// Test empty keys
	t.Run("empty keys", func(t *testing.T) {
		emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(&JWKS{Keys: []JWK{}})
		}))
		defer emptyServer.Close()
		_, err := cache.Get(context.Background(), emptyServer.URL)
		if err == nil {
			t.Error("expected error for empty keys, got nil")
		}
	})
}

// TestJWKSCacheExpiration tests cache expiration.
func TestJWKSCacheExpiration(t *testing.T) {
	testJWKS := &JWKS{
		Keys: []JWK{{KeyID: "test", KeyType: "RSA", N: "test", E: "AQAB"}},
	}
	var requestCount int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(testJWKS)
	}))
	defer server.Close()
	cache := NewJWKSCache(server.Client())
	// First fetch
	_, err := cache.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	// Manually expire the cache entry
	cache.mu.Lock()
	if entry, ok := cache.entries[server.URL]; ok {
		entry.expiresAt = time.Now().Add(-1 * time.Hour)
	}
	cache.mu.Unlock()
	// Next fetch should hit server again
	_, err = cache.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Get after expiration failed: %v", err)
	}
	if requestCount != 2 {
		t.Errorf("expected 2 requests after expiration, got %d", requestCount)
	}
}
