// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthServerMetadataParse(t *testing.T) {
	// Verify that we parse Google's auth server metadata.
	data, err := os.ReadFile(filepath.FromSlash("testdata/google-auth-meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a AuthServerMetadata
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	// Spot check.
	if g, w := a.Issuer, "https://accounts.google.com"; g != w {
		t.Errorf("got %q, want %q", g, w)
	}
}

func TestAuthClientMetadataParse(t *testing.T) {
	// Verify that we can parse a typical client metadata JSON.
	data, err := os.ReadFile(filepath.FromSlash("testdata/client-auth-meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a AuthClientMetadata
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	// Spot check
	if g, w := a.ClientName, "My Test App"; g != w {
		t.Errorf("got ClientName %q, want %q", g, w)
	}
	if g, w := len(a.RedirectURIs), 2; g != w {
		t.Errorf("got %d RedirectURIs, want %d", g, w)
	}
}

func TestRegisterClient(t *testing.T) {
	testCases := []struct {
		name         string
		handler      http.HandlerFunc
		clientMeta   *AuthClientMetadata
		wantClientID string
		wantErr      string
	}{
		{
			name: "Success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Errorf("Expected POST, got %s", r.Method)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				var receivedMeta AuthClientMetadata
				if err := json.Unmarshal(body, &receivedMeta); err != nil {
					t.Fatalf("Failed to unmarshal request body: %v", err)
				}
				if receivedMeta.ClientName != "Test App" {
					t.Errorf("Expected ClientName 'Test App', got '%s'", receivedMeta.ClientName)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{"client_id":"test-client-id","client_secret":"test-client-secret","client_name":"Test App"}`))
			},
			clientMeta:   &AuthClientMetadata{ClientName: "Test App", RedirectURIs: []string{"http://localhost/cb"}},
			wantClientID: "test-client-id",
		},
		{
			name: "Error - Missing ClientID in Response",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(`{"client_secret":"test-client-secret"}`)) // No client_id
			},
			clientMeta: &AuthClientMetadata{RedirectURIs: []string{"http://localhost/cb"}},
			wantErr:    "registration response is missing required 'client_id' field",
		},
		{
			name: "Error - Standard OAuth Error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":"invalid_redirect_uri","error_description":"Redirect URI is not valid."}`))
			},
			clientMeta: &AuthClientMetadata{RedirectURIs: []string{"http://invalid/cb"}},
			wantErr:    "registration failed: invalid_redirect_uri (Redirect URI is not valid.)",
		},
		{
			name: "Error - Non-JSON Server Error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Internal Server Error"))
			},
			clientMeta: &AuthClientMetadata{RedirectURIs: []string{"http://localhost/cb"}},
			wantErr:    "registration failed with status 500 Internal Server Error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			serverMeta := &AuthServerMetadata{RegistrationEndpoint: server.URL}
			info, err := RegisterClient(context.Background(), serverMeta, tc.clientMeta, server.Client())

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Expected an error containing '%s', but got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Expected error to contain '%s', got '%v'", tc.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Expected no error, but got: %v", err)
			}
			if info.ClientID != tc.wantClientID {
				t.Errorf("Expected client_id '%s', got '%s'", tc.wantClientID, info.ClientID)
			}
		})
	}

	t.Run("Error - No Endpoint in Metadata", func(t *testing.T) {
		serverMeta := &AuthServerMetadata{Issuer: "http://localhost"} // No RegistrationEndpoint
		_, err := RegisterClient(context.Background(), serverMeta, &AuthClientMetadata{}, nil)
		if err == nil {
			t.Fatal("Expected an error for missing registration endpoint, got nil")
		}
		expectedErr := "server metadata does not contain a registration_endpoint"
		if err.Error() != expectedErr {
			t.Errorf("Expected error '%s', got '%v'", expectedErr, err)
		}
	})
}
