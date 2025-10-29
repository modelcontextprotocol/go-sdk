// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package auth

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// A basicReader is an io.Reader to be used as a non-rereadable request body.
//
// net/http has special handling for strings.Reader that we want to avoid.
type basicReader struct {
	r *strings.Reader
}

func (r *basicReader) Read(p []byte) (n int, err error) { return r.r.Read(p) }

// TestHTTPTransport validates the OAuth HTTPTransport.
func TestHTTPTransport(t *testing.T) {
	const testToken = "test-token-123"
	fakeTokenSource := oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: testToken,
		TokenType:   "Bearer",
	})

	// authServer simulates a resource that requires OAuth.
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Ensure that the body was properly cloned, by reading it completely.
			// If the body is not cloned, reading it the second time may yield no
			// bytes.
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if len(body) == 0 {
				http.Error(w, "empty body", http.StatusBadRequest)
				return
			}
		}
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
		handler := func(req *http.Request, res *http.Response) (oauth2.TokenSource, error) {
			handlerCalls++
			// Verify that the response has the expected WWW-Authenticate header
			if res.Header.Get("WWW-Authenticate") != `Bearer resource_metadata="http://metadata.example.com"` {
				t.Errorf("handler got WWW-Authenticate header %q", res.Header.Get("WWW-Authenticate"))
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

	t.Run("request body is cloned", func(t *testing.T) {
		handler := func(req *http.Request, res *http.Response) (oauth2.TokenSource, error) {
			// Verify that the response has the expected WWW-Authenticate header
			if res.Header.Get("WWW-Authenticate") != `Bearer resource_metadata="http://metadata.example.com"` {
				t.Errorf("handler got WWW-Authenticate header %q", res.Header.Get("WWW-Authenticate"))
			}
			return fakeTokenSource, nil
		}

		transport, err := NewHTTPTransport(handler, nil)
		if err != nil {
			t.Fatalf("NewHTTPTransport() failed: %v", err)
		}
		client := &http.Client{Transport: transport}

		resp, err := client.Post(authServer.URL, "application/json", &basicReader{strings.NewReader("{}")})
		if err != nil {
			t.Fatalf("client.Post() failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("got status %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("handler returns error", func(t *testing.T) {
		handlerErr := errors.New("user rejected auth")
		handler := func(req *http.Request, res *http.Response) (oauth2.TokenSource, error) {
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
