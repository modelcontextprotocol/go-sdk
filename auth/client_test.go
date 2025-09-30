// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

// TestHTTPTransport validates the OAuth HTTPTransport.
func TestHTTPTransport(t *testing.T) {
	const testToken = "test-token-123"
	fakeTokenSource := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: testToken,
		TokenType:   "Bearer",
	})

	// authServer simulates a resource that requires OAuth.
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == fmt.Sprintf("Bearer %s", testToken) {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://metadata.example.com"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authServer.Close()

	t.Run("successful auth flow", func(t *testing.T) {
		var handlerCalls int
		handler := func(ctx context.Context, args OAuthHandlerArgs) (oauth2.TokenSource, error) {
			handlerCalls++
			if args.ResourceMetadataURL != "http://metadata.example.com" {
				t.Errorf("handler got metadata URL %q, want %q", args.ResourceMetadataURL, "http://metadata.example.com")
			}
			return fakeTokenSource, nil
		}

		transport, err := NewHTTPTransport(handler, nil)
		if err != nil {
			t.Fatalf("NewHTTPTransport() failed: %v", err)
		}
		client := &http.Client{Transport: transport}

		resp, err := client.Get(authServer.URL)
		if err != nil {
			t.Fatalf("client.Get() failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("got status %d, want %d", resp.StatusCode, http.StatusOK)
		}
		if handlerCalls != 1 {
			t.Errorf("handler was called %d times, want 1", handlerCalls)
		}

		// Second request should reuse the token and not call the handler again.
		resp2, err := client.Get(authServer.URL)
		if err != nil {
			t.Fatalf("second client.Get() failed: %v", err)
		}
		defer resp2.Body.Close()

		if resp2.StatusCode != http.StatusOK {
			t.Errorf("second request got status %d, want %d", resp2.StatusCode, http.StatusOK)
		}
		if handlerCalls != 1 {
			t.Errorf("handler should still be called only once, but was %d", handlerCalls)
		}
	})

	t.Run("handler returns error", func(t *testing.T) {
		handlerErr := errors.New("user rejected auth")
		handler := func(ctx context.Context, args OAuthHandlerArgs) (oauth2.TokenSource, error) {
			return nil, handlerErr
		}

		transport, err := NewHTTPTransport(handler, nil)
		if err != nil {
			t.Fatalf("NewHTTPTransport() failed: %v", err)
		}
		client := &http.Client{Transport: transport}

		_, err = client.Get(authServer.URL)
		if !errors.Is(err, handlerErr) {
			t.Errorf("client.Get() returned error %v, want %v", err, handlerErr)
		}
	})
}
