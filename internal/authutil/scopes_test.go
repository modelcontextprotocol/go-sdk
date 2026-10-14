// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.
package authutil

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oauth2"
)

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

func TestScopesFromToken(t *testing.T) {
	tests := []struct {
		name  string
		token *oauth2.Token
		want  []string
	}{
		{
			name:  "no scope",
			token: &oauth2.Token{AccessToken: "t"},
			want:  nil,
		},
		{
			name:  "single scope",
			token: (&oauth2.Token{AccessToken: "t"}).WithExtra(map[string]any{"scope": "read"}),
			want:  []string{"read"},
		},
		{
			name:  "space-delimited scopes",
			token: (&oauth2.Token{AccessToken: "t"}).WithExtra(map[string]any{"scope": "read  write admin"}),
			want:  []string{"read", "write", "admin"},
		},
		{
			name:  "form-encoded response",
			token: (&oauth2.Token{AccessToken: "t"}).WithExtra(url.Values{"scope": {"read write"}}),
			want:  []string{"read", "write"},
		},
		{
			name:  "non-string scope",
			token: (&oauth2.Token{AccessToken: "t"}).WithExtra(map[string]any{"scope": 42.0}),
			want:  nil,
		},
		// An empty scope names no scope-token (RFC 6749 section 3.3), so it
		// carries no more information than an absent one. Callers rely on nil
		// to fall back to the requested scopes.
		{
			name:  "empty scope",
			token: (&oauth2.Token{AccessToken: "t"}).WithExtra(map[string]any{"scope": ""}),
			want:  nil,
		},
		{
			name:  "whitespace-only scope",
			token: (&oauth2.Token{AccessToken: "t"}).WithExtra(map[string]any{"scope": " \t "}),
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScopesFromToken(tt.token)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ScopesFromToken() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
