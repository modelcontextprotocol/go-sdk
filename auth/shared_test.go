// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"net/http"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/oauthtest"
)

func TestAuthorizationServerMetadataURLs(t *testing.T) {
	tests := []struct {
		issuerURL string
		want      []string
	}{
		{
			issuerURL: "https://auth.example.com",
			want: []string{
				"https://auth.example.com/.well-known/oauth-authorization-server",
				"https://auth.example.com/.well-known/openid-configuration",
			},
		},
		{
			issuerURL: "https://auth.example.com/",
			want: []string{
				"https://auth.example.com/.well-known/oauth-authorization-server",
				"https://auth.example.com/.well-known/openid-configuration",
			},
		},
		{
			issuerURL: "https://auth.example.com/tenant1",
			want: []string{
				"https://auth.example.com/.well-known/oauth-authorization-server/tenant1",
				"https://auth.example.com/.well-known/openid-configuration/tenant1",
				"https://auth.example.com/tenant1/.well-known/openid-configuration",
			},
		},
		{
			issuerURL: "https://auth.example.com/tenant1/",
			want: []string{
				"https://auth.example.com/.well-known/oauth-authorization-server/tenant1",
				"https://auth.example.com/.well-known/openid-configuration/tenant1",
				"https://auth.example.com/tenant1/.well-known/openid-configuration",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.issuerURL, func(t *testing.T) {
			if got := authorizationServerMetadataURLs(tt.issuerURL); !slices.Equal(got, tt.want) {
				t.Errorf("authorizationServerMetadataURLs(%q) =\n%v\nwant\n%v", tt.issuerURL, got, tt.want)
			}
		})
	}
}

// TestGetAuthServerMetadataTrailingSlashIssuer checks that an issuer identifier
// written with a terminating slash still finds metadata served at the root
// well-known path.
func TestGetAuthServerMetadataTrailingSlashIssuer(t *testing.T) {
	s := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{
		MetadataEndpointConfig: &oauthtest.MetadataEndpointConfig{
			ServeOAuthInsertedEndpoint: true,
		},
	})
	s.Start(t)

	got, err := GetAuthServerMetadata(t.Context(), s.URL()+"/", http.DefaultClient)
	if err != nil {
		t.Fatalf("GetAuthServerMetadata() error = %v, want nil", err)
	}
	if got == nil {
		t.Fatal("GetAuthServerMetadata() got nil, want metadata")
	}
	if got.Issuer != s.URL() {
		t.Errorf("GetAuthServerMetadata() issuer = %q, want %q", got.Issuer, s.URL())
	}
}

func TestGetAuthServerMetadata(t *testing.T) {
	tests := []struct {
		name           string
		issuerPath     string
		endpointConfig *oauthtest.MetadataEndpointConfig
		wantNil        bool
	}{
		{
			name:       "OAuthEndpoint_Root",
			issuerPath: "",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOAuthInsertedEndpoint: true,
			},
		},
		{
			name:       "OpenIDEndpoint_Root",
			issuerPath: "",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOpenIDInsertedEndpoint: true,
			},
		},
		{
			name:       "OAuthEndpoint_Path",
			issuerPath: "/oauth",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOAuthInsertedEndpoint: true,
			},
		},
		{
			name:       "OpenIDEndpoint_Path",
			issuerPath: "/openid",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOpenIDInsertedEndpoint: true,
			},
		},
		{
			name:       "OpenIDAppendedEndpoint_Path",
			issuerPath: "/openid",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				ServeOpenIDAppendedEndpoint: true,
			},
		},
		{
			name:       "NoMetadata",
			issuerPath: "",
			endpointConfig: &oauthtest.MetadataEndpointConfig{
				// All metadata endpoints disabled.
				ServeOAuthInsertedEndpoint:  false,
				ServeOpenIDInsertedEndpoint: false,
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := oauthtest.NewFakeAuthorizationServer(oauthtest.Config{
				IssuerPath:             tt.issuerPath,
				MetadataEndpointConfig: tt.endpointConfig,
			})
			s.Start(t)
			issuerURL := s.URL() + tt.issuerPath

			got, err := GetAuthServerMetadata(t.Context(), issuerURL, http.DefaultClient)
			if tt.wantNil {
				// When no metadata is found, GetAuthServerMetadata returns (nil, nil).
				if err != nil {
					t.Fatalf("GetAuthServerMetadata() unexpected error = %v, want nil", err)
				}
				if got != nil {
					t.Fatal("GetAuthServerMetadata() expected nil for no metadata, got metadata")
				}
				return
			}
			if err != nil {
				t.Fatalf("GetAuthServerMetadata() error = %v, want nil", err)
			}
			if got == nil {
				t.Fatal("GetAuthServerMetadata() got nil, want metadata")
			}
			if got.Issuer != issuerURL {
				t.Errorf("GetAuthServerMetadata() issuer = %q, want %q", got.Issuer, issuerURL)
			}
		})
	}
}
