// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"encoding/json"
	"os"
	"path/filepath"
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
