// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package authutil

import (
	"strings"
	"testing"
)

func TestCheckPKCESupport(t *testing.T) {
	for _, tt := range []struct {
		name    string
		methods []string
		wantErr string // empty if no error is expected
	}{
		{"S256", []string{"S256"}, ""},
		{"plain and S256", []string{"plain", "S256"}, ""},
		{"no methods", nil, "does not implement PKCE"},
		// The SDK always sends an S256 code challenge, so a server that offers
		// only plain cannot complete the flow.
		{"plain only", []string{"plain"}, "does not support the S256 PKCE method"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPKCESupport(tt.methods, "https://auth.example.com")
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("CheckPKCESupport(%v) = %v, want nil", tt.methods, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("CheckPKCESupport(%v) = %v, want an error containing %q", tt.methods, err, tt.wantErr)
			}
		})
	}
}
