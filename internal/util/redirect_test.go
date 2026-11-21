// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package util

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckRedirectWithinOrigin(t *testing.T) {
	newRequest := func(method, u string) *http.Request {
		t.Helper()
		r, err := http.NewRequest(method, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	get := func(u string) *http.Request { return newRequest(http.MethodGet, u) }
	post := func(u string) *http.Request { return newRequest(http.MethodPost, u) }

	tests := []struct {
		name    string
		via     []*http.Request
		req     *http.Request
		wantErr bool
	}{
		{"same origin", []*http.Request{post("https://example.com/mcp")}, post("https://example.com/other"), false},
		{"host case", []*http.Request{post("https://example.com/mcp")}, post("https://EXAMPLE.com/mcp"), false},
		{"explicit default port", []*http.Request{post("http://example.com:80/mcp")}, post("http://example.com/mcp"), false},
		{"upgrade to https", []*http.Request{post("http://example.com/mcp")}, post("https://example.com/mcp"), false},
		{"other host", []*http.Request{post("https://example.com/mcp")}, post("https://evil.example/mcp"), true},
		{"subdomain", []*http.Request{post("https://example.com/mcp")}, post("https://www.example.com/mcp"), true},
		{"other port", []*http.Request{post("https://example.com/mcp")}, post("https://example.com:8443/mcp"), true},
		{"downgrade to http", []*http.Request{post("https://example.com/mcp")}, post("http://example.com/mcp"), true},
		{"upgrade from non-default port", []*http.Request{post("http://example.com:8080/mcp")}, post("https://example.com/mcp"), true},
		{"method change", []*http.Request{post("https://example.com/mcp")}, get("https://example.com/mcp"), true},
		{"get stays get", []*http.Request{get("https://example.com/mcp")}, get("https://example.com/other"), false},
		{
			// Compared with the request it redirects, not the first one.
			name:    "downgrade after upgrade",
			via:     []*http.Request{get("http://example.com/a"), get("https://example.com/b")},
			req:     get("http://example.com/c"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkRedirectWithinOrigin(tt.req, tt.via)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkRedirectWithinOrigin(%s %s) = %v, want error: %t", tt.req.Method, tt.req.URL, err, tt.wantErr)
			}
		})
	}
}

func TestRestrictRedirects(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		c := RestrictRedirects(nil)
		if c == http.DefaultClient || c.CheckRedirect == nil {
			t.Fatalf("RestrictRedirects(nil) = %+v, want a copy of http.DefaultClient with CheckRedirect", c)
		}
		if http.DefaultClient.CheckRedirect != nil {
			t.Fatal("RestrictRedirects(nil) modified http.DefaultClient")
		}
	})
	t.Run("copy", func(t *testing.T) {
		orig := &http.Client{Timeout: time.Minute}
		c := RestrictRedirects(orig)
		if c == orig || c.CheckRedirect == nil || c.Timeout != time.Minute {
			t.Fatalf("RestrictRedirects() = %+v, want a copy with CheckRedirect", c)
		}
		if orig.CheckRedirect != nil {
			t.Fatal("RestrictRedirects() modified its argument")
		}
	})
	t.Run("custom CheckRedirect", func(t *testing.T) {
		orig := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
		if c := RestrictRedirects(orig); c != orig {
			t.Fatal("RestrictRedirects() replaced a client that sets CheckRedirect")
		}
	})
	t.Run("followallredirects", func(t *testing.T) {
		old := followallredirects
		followallredirects = "1"
		t.Cleanup(func() { followallredirects = old })

		orig := &http.Client{}
		if c := RestrictRedirects(orig); c != orig {
			t.Fatal("RestrictRedirects() replaced the client with followallredirects=1")
		}
		if c := RestrictRedirects(nil); c != http.DefaultClient {
			t.Fatal("RestrictRedirects(nil) did not return http.DefaultClient with followallredirects=1")
		}
	})
}

func TestRestrictRedirectsRequests(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
	}))
	t.Cleanup(target.Close)

	var originHits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
		case "/found":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/cross":
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
		case "/final":
			io.Copy(w, r.Body)
		}
	}))
	t.Cleanup(origin.Close)

	client := RestrictRedirects(origin.Client())
	do := func(method, path, body string) (string, error) {
		t.Helper()
		req, err := http.NewRequest(method, origin.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		return string(got), err
	}

	if got, err := do(http.MethodPost, "/same", "hello"); err != nil || got != "hello" {
		t.Errorf("POST after a same-origin 307 = %q, %v, want %q", got, err, "hello")
	}
	if _, err := do(http.MethodGet, "/found", ""); err != nil {
		t.Errorf("GET after a same-origin 302: %v", err)
	}
	if _, err := do(http.MethodPost, "/found", "hello"); err == nil || !strings.Contains(err.Error(), "changes the method") {
		t.Errorf("POST after a 302 = %v, want method change error", err)
	}
	if _, err := do(http.MethodPost, "/cross", "hello"); err == nil || !strings.Contains(err.Error(), "outside the origin") {
		t.Errorf("POST after a cross-origin 307 = %v, want origin error", err)
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("cross-origin redirect target got %d requests, want 0", n)
	}

	originHits.Store(0)
	if _, err := do(http.MethodGet, "/loop", ""); err == nil || !strings.Contains(err.Error(), "stopped after") {
		t.Errorf("GET of a redirect loop = %v, want redirect limit error", err)
	}
	if got, want := originHits.Load(), int32(maxRedirects); got != want {
		t.Errorf("redirect loop sent %d requests, want %d", got, want)
	}
}
