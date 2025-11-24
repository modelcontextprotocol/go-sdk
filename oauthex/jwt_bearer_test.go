// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestExchangeJWTBearer tests the JWT Bearer grant flow.
func TestExchangeJWTBearer(t *testing.T) {
	// Create a test MCP Server auth server that accepts JWT Bearer grants
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and content type
		if r.Method != http.MethodPost {
			t.Errorf("expected POST request, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/x-www-form-urlencoded" {
			t.Errorf("expected application/x-www-form-urlencoded, got %s", contentType)
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}
		// Parse form data
		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}
		// Verify grant type per RFC 7523
		grantType := r.FormValue("grant_type")
		if grantType != GrantTypeJWTBearer {
			t.Errorf("expected grant_type %s, got %s", GrantTypeJWTBearer, grantType)
			writeJWTBearerErrorResponse(w, "unsupported_grant_type", "grant type not supported")
			return
		}
		// Verify assertion is provided
		assertion := r.FormValue("assertion")
		if assertion == "" {
			t.Error("assertion is required")
			writeJWTBearerErrorResponse(w, "invalid_request", "missing assertion")
			return
		}
		// Verify client authentication
		clientID := r.FormValue("client_id")
		clientSecret := r.FormValue("client_secret")
		if clientID == "" || clientSecret == "" {
			t.Error("client authentication required")
			writeJWTBearerErrorResponse(w, "invalid_client", "client authentication failed")
			return
		}
		if clientID != "mcp-client-id" || clientSecret != "mcp-client-secret" {
			t.Error("invalid client credentials")
			writeJWTBearerErrorResponse(w, "invalid_client", "invalid credentials")
			return
		}
		// Return successful OAuth token response
		resp := JWTBearerResponse{
			AccessToken:  "mcp-access-token-123",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			Scope:        "read write",
			RefreshToken: "mcp-refresh-token-456",
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()
	// Test successful JWT Bearer grant
	t.Run("successful exchange", func(t *testing.T) {
		token, err := ExchangeJWTBearer(
			context.Background(),
			server.URL,
			"fake-id-jag-jwt",
			"mcp-client-id",
			"mcp-client-secret",
			server.Client(),
		)
		if err != nil {
			t.Fatalf("ExchangeJWTBearer failed: %v", err)
		}
		if token.AccessToken != "mcp-access-token-123" {
			t.Errorf("expected access_token 'mcp-access-token-123', got %s", token.AccessToken)
		}
		if token.TokenType != "Bearer" {
			t.Errorf("expected token_type 'Bearer', got %s", token.TokenType)
		}
		if token.RefreshToken != "mcp-refresh-token-456" {
			t.Errorf("expected refresh_token 'mcp-refresh-token-456', got %s", token.RefreshToken)
		}
		// Check expiration (should be ~1 hour from now)
		expectedExpiry := time.Now().Add(3600 * time.Second)
		if token.Expiry.Before(time.Now()) || token.Expiry.After(expectedExpiry.Add(5*time.Second)) {
			t.Errorf("unexpected expiry time: %v", token.Expiry)
		}
		// Check scope in extra data
		scope, ok := token.Extra("scope").(string)
		if !ok || scope != "read write" {
			t.Errorf("expected scope 'read write', got %v", token.Extra("scope"))
		}
	})
	// Test missing assertion
	t.Run("missing assertion", func(t *testing.T) {
		_, err := ExchangeJWTBearer(
			context.Background(),
			server.URL,
			"", // empty assertion
			"mcp-client-id",
			"mcp-client-secret",
			server.Client(),
		)
		if err == nil {
			t.Error("expected error for missing assertion, got nil")
		}
	})
	// Test invalid URL scheme
	t.Run("invalid token endpoint URL", func(t *testing.T) {
		_, err := ExchangeJWTBearer(
			context.Background(),
			"javascript:alert(1)",
			"fake-id-jag-jwt",
			"mcp-client-id",
			"mcp-client-secret",
			server.Client(),
		)
		if err == nil {
			t.Error("expected error for invalid URL scheme, got nil")
		}
	})
}

// writeJWTBearerErrorResponse writes an OAuth 2.0 error response per RFC 6749 Section 5.2.
func writeJWTBearerErrorResponse(w http.ResponseWriter, errorCode, errorDescription string) {
	errResp := JWTBearerError{
		ErrorCode:        errorCode,
		ErrorDescription: errorDescription,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(errResp)
}
