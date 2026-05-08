// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package auth

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/internal/oauthtest"
)

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

func TestUnionScopes(t *testing.T) {
	tests := []struct {
		name       string
		existing   []string
		challenged []string
		want       []string
	}{
		{
			name:       "both empty",
			existing:   nil,
			challenged: nil,
			want:       nil,
		},
		{
			name:       "existing only",
			existing:   []string{"read"},
			challenged: nil,
			want:       []string{"read"},
		},
		{
			name:       "challenged only",
			existing:   nil,
			challenged: []string{"write"},
			want:       []string{"write"},
		},
		{
			name:       "disjoint scopes",
			existing:   []string{"read"},
			challenged: []string{"write"},
			want:       []string{"read", "write"},
		},
		{
			name:       "overlapping scopes",
			existing:   []string{"read", "write"},
			challenged: []string{"write", "admin"},
			want:       []string{"read", "write", "admin"},
		},
		{
			name:       "identical scopes",
			existing:   []string{"read", "write"},
			challenged: []string{"read", "write"},
			want:       []string{"read", "write"},
		},
		{
			name:       "mixed scopes",
			existing:   []string{"b", "a"},
			challenged: []string{"c", "a"},
			want:       []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnionScopes(tt.existing, tt.challenged)
			if diff := cmp.Diff(tt.want, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("UnionScopes() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
