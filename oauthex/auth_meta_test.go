// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build mcp_go_client_oauth

package oauthex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestGetAuthServerMetaRequirePKCE(t *testing.T) {
	ctx := context.Background()

	// Start a fake OAuth 2.1 auth server that advertises PKCE (S256).
	wrapper := http.NewServeMux()
	wrapper.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		NewFakeMCPServerMux().ServeHTTP(w, r)
	})
	ts := httptest.NewTLSServer(wrapper)
	defer ts.Close()

	// Validate that the server supports PKCE per MCP auth requirements.
	// The fake server sets issuer to https://localhost:<port>, so compute that issuer.
	u, _ := url.Parse(ts.URL)
	issuer := "https://localhost:" + u.Port()

	// The fake server presents a cert for example.com; set ServerName accordingly.
	httpClient := ts.Client()
	if tr, ok := httpClient.Transport.(*http.Transport); ok {
		clone := tr.Clone()
		clone.TLSClientConfig.ServerName = "example.com"
		httpClient.Transport = clone
	}

	if _, err := GetAuthServerMeta(ctx, issuer, httpClient); err != nil {
		t.Fatal(err)
	}

}
