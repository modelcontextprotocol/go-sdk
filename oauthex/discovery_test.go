// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckHTTPSOrLoopback(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{name: "empty", addr: "", wantErr: false},
		{name: "https dns allowed", addr: "https://example.com/prm", wantErr: false},
		{name: "cleartext dns disallowed", addr: "http://example.com/prm", wantErr: true},
		{name: "https loopback ip allowed", addr: "http://127.0.0.1:8080/prm", wantErr: false},
		{name: "http localhost allowed", addr: "http://localhost/prm", wantErr: false},
		{name: "https private ip disallowed", addr: "https://10.0.0.1/prm", wantErr: true},
		{name: "link local metadata disallowed", addr: "https://169.254.169.254/", wantErr: true},
		{name: "mapped private address disallowed", addr: "https://[::ffff:10.0.0.1]/prm", wantErr: true},
		{name: "https public ip allowed", addr: "https://8.8.8.8/prm", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkHTTPSOrLoopback(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkHTTPSOrLoopback(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}

func TestDiscoveryClientRedirect(t *testing.T) {
	newRequestTo := func(u string) *http.Request {
		t.Helper()
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatalf("NewRequest(%q): %v", u, err)
		}
		return r
	}
	testCases := []struct {
		name        string
		custom      *http.Client
		req         *http.Request
		via         []*http.Request
		wantBlocked bool
	}{
		{
			name:        "redirect to public allowed",
			req:         newRequestTo("https://8.8.8.8/"),
			via:         []*http.Request{newRequestTo("https://93.184.216.34/")},
			wantBlocked: false,
		},
		{
			name:        "downgrade blocked",
			req:         newRequestTo("http://93.184.216.34/"),
			via:         []*http.Request{newRequestTo("https://93.184.216.34/")},
			wantBlocked: true,
		},
		{
			name:        "redirect to loopback blocked",
			req:         newRequestTo("https://127.0.0.1/"),
			via:         []*http.Request{newRequestTo("https://93.184.216.34/")},
			wantBlocked: true,
		},
		{
			name:        "custom client with no custom check",
			custom:      &http.Client{},
			req:         newRequestTo("https://127.0.0.1/"),
			via:         []*http.Request{newRequestTo("https://93.184.216.34/")},
			wantBlocked: true,
		},
		{
			name: "custom client check disable",
			custom: &http.Client{
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return nil
				},
			},
			req:         newRequestTo("https://127.0.0.1/"),
			via:         []*http.Request{newRequestTo("https://93.184.216.34/")},
			wantBlocked: false,
		},
		{
			name: "too many redirects",
			req:  newRequestTo("https://8.8.8.8/"),
			via: func() []*http.Request {
				via := make([]*http.Request, maxDiscoveryRedirects)
				for i := range via {
					via[i] = newRequestTo("https://8.8.8.8/")
				}
				return via
			}(),
			wantBlocked: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := newDiscoveryClient(tc.custom)
			gotErr := client.CheckRedirect(tc.req, tc.via)
			if gotErr != nil && !tc.wantBlocked {
				t.Fatalf("client.CheckRedirect() error = %v", gotErr)
			}
			if gotErr == nil && tc.wantBlocked {
				t.Fatal("client.CheckRedirect() error = nil, want blocked")
			}
		})
	}
}

func TestDiscoveryBlocksInternalTarget(t *testing.T) {
	ctx := t.Context()
	for _, target := range []string{
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/.well-known/oauth-protected-resource",
	} {
		t.Run(target, func(t *testing.T) {
			_, err := GetProtectedResourceMetadata(ctx, target, target, nil)
			if err == nil {
				t.Fatalf("GetProtectedResourceMetadata(%q) succeeded, want SSRF rejection", target)
			}
			if !strings.Contains(err.Error(), "non-public") {
				t.Errorf("error = %v, want it to mention a non-public address", err)
			}
		})
	}
}

func TestDiscoveryAllowsLoopbackInitial(t *testing.T) {
	ctx := t.Context()
	h := &fakeResourceHandler{}
	server := httptest.NewTLSServer(h)
	defer server.Close()
	h.installHandlers(server.URL)

	metadataURL := server.URL + "/.well-known/oauth-protected-resource"
	prm, err := GetProtectedResourceMetadata(ctx, metadataURL, server.URL, server.Client())
	if err != nil {
		t.Fatalf("GetProtectedResourceMetadata on loopback failed: %v", err)
	}
	if prm == nil {
		t.Fatal("nil prm")
	}
}
