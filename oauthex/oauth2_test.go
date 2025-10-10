// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSplitChallenges(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single challenge no params",
			input: `Basic`,
			want:  []string{`Basic`},
		},
		{
			name:  "single challenge with params",
			input: `Bearer realm="example.com", error="invalid_token"`,
			want:  []string{`Bearer realm="example.com", error="invalid_token"`},
		},
		{
			name:  "single challenge with comma in quoted string",
			input: `Bearer realm="example, with comma"`,
			want:  []string{`Bearer realm="example, with comma"`},
		},
		{
			name:  "two challenges",
			input: `Basic, Bearer realm="example"`,
			want:  []string{`Basic`, ` Bearer realm="example"`},
		},
		{
			name:  "multiple challenges complex",
			input: `Newauth realm="apps", Basic, Bearer realm="example.com", error="invalid_token"`,
			want:  []string{`Newauth realm="apps"`, ` Basic`, ` Bearer realm="example.com", error="invalid_token"`},
		},
		{
			name:  "challenge with escaped quote",
			input: `Bearer realm="example \"quoted\""`,
			want:  []string{`Bearer realm="example \"quoted\""`},
		},
		{
			name:  "empty input",
			input: "",
			want:  []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitChallenges(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitChallenges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSplitChallengesError(t *testing.T) {
	if _, err := splitChallenges(`"Bearer"`); err == nil {
		t.Fatal("got nil, want error")
	}
}

func TestParseSingleChallenge(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    challenge
		wantErr bool
	}{
		{
			name:  "scheme only",
			input: "Basic",
			want: challenge{
				Scheme: "basic",
			},
			wantErr: false,
		},
		{
			name:  "scheme with one quoted param",
			input: `Bearer realm="example.com"`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:  "scheme with one unquoted param",
			input: `Bearer realm=example.com`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:  "scheme with multiple params",
			input: `Bearer realm="example", error="invalid_token", error_description="The token expired"`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{
					"realm":             "example",
					"error":             "invalid_token",
					"error_description": "The token expired",
				},
			},
			wantErr: false,
		},
		{
			name:  "scheme with multiple unquoted params",
			input: `Bearer realm=example, error=invalid_token, error_description=The token expired`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{
					"realm":             "example",
					"error":             "invalid_token",
					"error_description": "The token expired",
				},
			},
			wantErr: false,
		},
		{
			name:  "case-insensitive scheme and keys",
			input: `BEARER ReAlM="example"`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example"},
			},
			wantErr: false,
		},
		{
			name:  "param with escaped quote",
			input: `Bearer realm="example \"foo\" bar"`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": `example "foo" bar`},
			},
			wantErr: false,
		},
		{
			name:  "param without quotes (token)",
			input: "Bearer realm=example.com",
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:    "malformed param - no value",
			input:   "Bearer realm=",
			wantErr: true,
		},
		{
			name:    "malformed param - unterminated quote",
			input:   `Bearer realm="example`,
			wantErr: true,
		},
		{
			name:    "malformed param - missing comma",
			input:   `Bearer realm="a" error="b"`,
			wantErr: true,
		},
		{
			name:    "malformed param - initial equal",
			input:   `Bearer ="a"`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSingleChallenge(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSingleChallenge() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSingleChallenge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetProtectedResourceMetadata(t *testing.T) {
	ctx := context.Background()
	t.Run("FromHeader", func(t *testing.T) {
		h := &fakeResourceHandler{serveWWWAuthenticate: true}
		server := httptest.NewTLSServer(h)
		h.installHandlers(server.URL)
		client := server.Client()
		serverURL := server.URL + "/resource"
		res, err := client.Get(serverURL)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatal("want unauth")
		}
		prm, err := GetProtectedResourceMetadataFromHeader(ctx, serverURL, res.Header, client)
		if err != nil {
			t.Fatal(err)
		}
		if prm == nil {
			t.Fatal("nil prm")
		}
	})
	t.Run("FromID", func(t *testing.T) {
		h := &fakeResourceHandler{serveWWWAuthenticate: false}
		server := httptest.NewTLSServer(h)
		h.installHandlers(server.URL)
		client := server.Client()
		prm, err := GetProtectedResourceMetadataFromID(ctx, server.URL, client)
		if err != nil {
			t.Fatal(err)
		}
		if prm == nil {
			t.Fatal("nil prm")
		}
	})
	// Test that metadata URL and resource identifier are properly distinguished (issue #560).
	t.Run("FromHeaderValidatesAgainstServerURL", func(t *testing.T) {
		h := &fakeResourceHandler{serveWWWAuthenticate: true}
		server := httptest.NewTLSServer(h)
		h.installHandlers(server.URL)
		client := server.Client()
		serverURL := server.URL + "/resource"
		res, err := client.Get(serverURL)
		if err != nil {
			t.Fatal(err)
		}
		// This should succeed because we validate against serverURL, not metadataURL
		prm, err := GetProtectedResourceMetadataFromHeader(ctx, serverURL, res.Header, client)
		if err != nil {
			t.Fatalf("Expected validation to succeed, got error: %v", err)
		}
		if prm == nil {
			t.Fatal("Expected non-nil prm")
		}
		if prm.Resource != serverURL {
			t.Errorf("Expected resource %q, got %q", serverURL, prm.Resource)
		}
	})
	t.Run("FromHeaderRejectsImpersonation", func(t *testing.T) {
		h := &fakeResourceHandler{serveWWWAuthenticate: true, resourceOverride: "https://attacker.com/evil"}
		server := httptest.NewTLSServer(h)
		h.installHandlers(server.URL)
		client := server.Client()
		serverURL := server.URL + "/resource"
		res, err := client.Get(serverURL)
		if err != nil {
			t.Fatal(err)
		}
		prm, err := GetProtectedResourceMetadataFromHeader(ctx, serverURL, res.Header, client)
		if err == nil {
			t.Fatal("Expected validation error for mismatched resource, got nil")
		}
		if prm != nil {
			t.Fatal("Expected nil prm on validation failure")
		}
	})
}

type fakeResourceHandler struct {
	http.ServeMux
	serveWWWAuthenticate bool
	resourceOverride     string // If set, use this instead of correct resource (for testing validation)
}

func (h *fakeResourceHandler) installHandlers(serverURL string) {
	path := "/.well-known/oauth-protected-resource"
	url := serverURL + path
	h.Handle("GET /resource", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.serveWWWAuthenticate {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, url))
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	h.Handle("GET "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Per RFC 9728 section 3.3, the resource field should contain the actual resource identifier,
		// which is the URL the client uses to access the resource (serverURL + "/resource" for WWW-Authenticate case).
		// For the FromID test case, it's just the serverURL.
		resource := serverURL
		if h.serveWWWAuthenticate {
			resource = serverURL + "/resource"
		}
		// Allow testing with custom resource values (e.g., impersonation attacks).
		if h.resourceOverride != "" {
			resource = h.resourceOverride
		}
		prm := &ProtectedResourceMetadata{Resource: resource}
		if err := json.NewEncoder(w).Encode(prm); err != nil {
			panic(err)
		}
	}))
}
