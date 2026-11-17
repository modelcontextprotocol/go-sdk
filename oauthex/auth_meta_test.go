// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAuthMetaParse(t *testing.T) {
	// Verify that we parse Google's auth server metadata.
	data, err := os.ReadFile(filepath.FromSlash("testdata/google-auth-meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a AuthServerMeta
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	// Spot check.
	if g, w := a.Issuer, "https://accounts.google.com"; g != w {
		t.Errorf("got %q, want %q", g, w)
	}
}

// TestGetAuthServerMetaPKCEMethods verifies that GetAuthServerMeta returns the
// advertised PKCE methods without judging them. Only the authorization code
// flows need PKCE, and they check it themselves; a server that serves only the
// client credentials or JWT bearer grant does not advertise it.
func TestGetAuthServerMetaPKCEMethods(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name                    string
		hasPKCESupport          bool
		pkceMethods             []string // overrides the default ["S256"] when set
		issuerWithTrailingSlash bool
	}{
		{
			name:           "server_with_pkce_support",
			hasPKCESupport: true,
		},
		{
			name:           "server_without_pkce_support",
			hasPKCESupport: false,
		},
		{
			name:           "server_with_only_plain_pkce",
			hasPKCESupport: true,
			pkceMethods:    []string{"plain"},
		},
		{
			name:           "server_with_plain_and_s256_pkce",
			hasPKCESupport: true,
			pkceMethods:    []string{"plain", "S256"},
		},
		{
			// ProtectedResourceMetadata may contain AuthorizationServers with a trailing slash (see Issue #953)
			name:                    "issuer_with_trailing_slash",
			hasPKCESupport:          true,
			issuerWithTrailingSlash: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Start a fake OAuth 2.1 auth server
			wrapper := http.NewServeMux()
			wrapper.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
				u, _ := url.Parse("https://" + r.Host)
				issuer := "https://localhost:" + u.Port()
				metadata := AuthServerMeta{
					Issuer:                            issuer,
					AuthorizationEndpoint:             issuer + "/authorize",
					TokenEndpoint:                     issuer + "/token",
					RegistrationEndpoint:              issuer + "/register",
					JWKSURI:                           issuer + "/.well-known/jwks.json",
					ScopesSupported:                   []string{"openid", "profile", "email"},
					ResponseTypesSupported:            []string{"code"},
					GrantTypesSupported:               []string{"authorization_code"},
					TokenEndpointAuthMethodsSupported: []string{"none"},
				}

				// Add PKCE support based on test case
				if tt.hasPKCESupport {
					metadata.CodeChallengeMethodsSupported = []string{"S256"}
					if tt.pkceMethods != nil {
						metadata.CodeChallengeMethodsSupported = tt.pkceMethods
					}
				}
				// If hasPKCESupport is false, CodeChallengeMethodsSupported remains empty

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(metadata)
			})
			ts := httptest.NewTLSServer(wrapper)
			defer ts.Close()

			// The fake server sets issuer to https://localhost:<port>, so compute that issuer.
			u, _ := url.Parse(ts.URL)
			issuer := "https://localhost:" + u.Port()
			metadataURL := issuer + "/.well-known/oauth-authorization-server"
			if tt.issuerWithTrailingSlash {
				issuer += "/"
			}

			// The fake server presents a cert for example.com; set ServerName accordingly.
			httpClient := ts.Client()
			if tr, ok := httpClient.Transport.(*http.Transport); ok {
				clone := tr.Clone()
				clone.TLSClientConfig.ServerName = "example.com"
				httpClient.Transport = clone
			}

			meta, err := GetAuthServerMeta(ctx, metadataURL, issuer, httpClient)
			if err != nil {
				t.Fatalf("unwanted error: %v", err)
			}
			if meta == nil {
				t.Fatal("wanted metadata but got nil")
			}
			var wantMethods []string
			if tt.hasPKCESupport {
				wantMethods = []string{"S256"}
				if tt.pkceMethods != nil {
					wantMethods = tt.pkceMethods
				}
			}
			if !slices.Equal(meta.CodeChallengeMethodsSupported, wantMethods) {
				t.Errorf("CodeChallengeMethodsSupported = %v, want %v", meta.CodeChallengeMethodsSupported, wantMethods)
			}
		})
	}
}
