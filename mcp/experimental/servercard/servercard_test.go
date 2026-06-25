// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package servercard

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testImplementation() *mcp.Implementation {
	return &mcp.Implementation{
		Name:       "dice-roller",
		Title:      "Dice Roller",
		Version:    "1.0.0",
		WebsiteURL: "https://example.com/dice",
		Icons: []mcp.Icon{{
			Source:   "https://example.com/icon.png",
			MIMEType: "image/png",
			Sizes:    []string{"48x48"},
		}},
	}
}

func TestBuildServerCard(t *testing.T) {
	card, err := BuildServerCard(testImplementation(),
		WithName("com.example/dice-roller"),
		WithDescription("Rolls dice."),
		WithRemotes(Remote{Type: RemoteTypeStreamableHTTP, URL: "https://dice.example.com/mcp"}),
		WithRepository(Repository{URL: "https://github.com/example/dice", Source: "github"}),
		WithMeta(map[string]any{"com.example/foo": "bar"}),
	)
	if err != nil {
		t.Fatalf("BuildServerCard() error = %v", err)
	}
	if card.Schema != SchemaURL {
		t.Fatalf("card.Schema = %q, want %q", card.Schema, SchemaURL)
	}
	if card.Name != "com.example/dice-roller" {
		t.Errorf("card.Name = %q", card.Name)
	}
	if card.Title != "Dice Roller" || card.Description != "Rolls dice." || card.Version != "1.0.0" || card.WebsiteURL != "https://example.com/dice" {
		t.Errorf("card identity = %+v", card)
	}
	if len(card.Remotes) != 1 || card.Remotes[0].URL != "https://dice.example.com/mcp" {
		t.Fatalf("card.Remotes = %+v", card.Remotes)
	}
	if card.Repository == nil || card.Repository.Source != "github" {
		t.Fatalf("card.Repository = %+v", card.Repository)
	}
	if card.Meta["com.example/foo"] != "bar" {
		t.Fatalf("card.Meta = %+v", card.Meta)
	}
}

func TestBuildServerCardValidation(t *testing.T) {
	tests := []struct {
		name string
		impl *mcp.Implementation
		opts []BuildOption
		want string
	}{
		{
			name: "nil implementation",
			want: "implementation",
		},
		{
			name: "missing card name",
			impl: testImplementation(),
			want: "name",
		},
		{
			name: "missing version",
			impl: &mcp.Implementation{Name: "x"},
			opts: []BuildOption{WithName("com.example/no-version"), WithDescription("desc")},
			want: "version",
		},
		{
			name: "missing description",
			impl: &mcp.Implementation{Name: "x", Version: "1.0.0"},
			opts: []BuildOption{WithName("com.example/no-description")},
			want: "description",
		},
		{
			name: "version range",
			impl: &mcp.Implementation{Name: "x", Version: ">=1.0.0"},
			opts: []BuildOption{WithName("com.example/range"), WithDescription("desc")},
			want: "exact version",
		},
		{
			name: "version wildcard",
			impl: &mcp.Implementation{Name: "x", Version: "1.x"},
			opts: []BuildOption{WithName("com.example/wildcard"), WithDescription("desc")},
			want: "exact version",
		},
		{
			name: "invalid name",
			impl: testImplementation(),
			opts: []BuildOption{WithName("no-slash"), WithDescription("desc")},
			want: "name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildServerCard(tt.impl, tt.opts...)
			if err == nil {
				t.Fatal("BuildServerCard() succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("BuildServerCard() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestExactVersionsAccepted(t *testing.T) {
	for _, version := range []string{"1.0.0", "1.0.0-x", "1.0.0-X.1", "1.0.0-rc.x", "2024-01-05"} {
		t.Run(version, func(t *testing.T) {
			impl := testImplementation()
			impl.Version = version
			card, err := BuildServerCard(impl, WithName("com.example/dice"), WithDescription("desc"))
			if err != nil {
				t.Fatalf("BuildServerCard() error = %v", err)
			}
			if card.Version != version {
				t.Fatalf("card.Version = %q, want %q", card.Version, version)
			}
		})
	}
}

func TestHandlerServesCardWithDiscoveryHeaders(t *testing.T) {
	card, err := BuildServerCard(testImplementation(),
		WithName("com.example/dice"),
		WithDescription("Rolls dice."),
		WithRemotes(Remote{Type: RemoteTypeStreamableHTTP, URL: "https://dice.example.com/mcp"}),
	)
	if err != nil {
		t.Fatalf("BuildServerCard() error = %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/server-card", nil)
	w := httptest.NewRecorder()
	Handler(card).ServeHTTP(w, r)
	res := w.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := res.Header.Get("Content-Type"); got != MediaType {
		t.Fatalf("Content-Type = %q, want %q", got, MediaType)
	}
	assertDiscoveryHeaders(t, res.Header)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	etag := res.Header.Get("ETag")
	if want := quotedSHA256(body); etag != want {
		t.Fatalf("ETag = %q, want %q", etag, want)
	}

	r = httptest.NewRequest(http.MethodGet, "/server-card", nil)
	w = httptest.NewRecorder()
	Handler(card).ServeHTTP(w, r)
	if got := w.Result().Header.Get("ETag"); got != etag {
		t.Fatalf("second ETag = %q, want stable ETag %q", got, etag)
	}

	var got ServerCard
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Schema != SchemaURL || got.Name != card.Name || got.Remotes[0].URL != card.Remotes[0].URL {
		t.Fatalf("response card = %+v, want %+v", got, card)
	}
}

func TestHandlerHandlesIfNoneMatch(t *testing.T) {
	card, err := BuildServerCard(testImplementation(),
		WithName("com.example/dice"),
		WithDescription("Rolls dice."),
		WithRemotes(Remote{Type: RemoteTypeStreamableHTTP, URL: "https://dice.example.com/mcp"}),
	)
	if err != nil {
		t.Fatalf("BuildServerCard() error = %v", err)
	}

	handler := Handler(card)
	status, header, body := serveServerCard(t, handler, "")
	if status != http.StatusOK {
		t.Fatalf("initial status = %d, want %d", status, http.StatusOK)
	}
	etag := header.Get("ETag")
	if etag == "" {
		t.Fatal("initial ETag is empty")
	}

	tests := []struct {
		name        string
		ifNoneMatch string
		wantStatus  int
		wantBody    []byte
	}{
		{
			name:        "matching strong tag",
			ifNoneMatch: etag,
			wantStatus:  http.StatusNotModified,
		},
		{
			name:        "matching weak tag",
			ifNoneMatch: "W/" + etag,
			wantStatus:  http.StatusNotModified,
		},
		{
			name:        "non-matching tag",
			ifNoneMatch: `"0000000000000000000000000000000000000000000000000000000000000000"`,
			wantStatus:  http.StatusOK,
			wantBody:    body,
		},
		{
			name:        "wildcard",
			ifNoneMatch: "*",
			wantStatus:  http.StatusNotModified,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, header, body := serveServerCard(t, handler, tt.ifNoneMatch)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			assertDiscoveryHeaders(t, header)
			if got := header.Get("ETag"); got != etag {
				t.Fatalf("ETag = %q, want %q", got, etag)
			}
			if string(body) != string(tt.wantBody) {
				t.Fatalf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestHandlerServesHeadWithETag(t *testing.T) {
	card, err := BuildServerCard(testImplementation(),
		WithName("com.example/dice"),
		WithDescription("Rolls dice."),
		WithRemotes(Remote{Type: RemoteTypeStreamableHTTP, URL: "https://dice.example.com/mcp"}),
	)
	if err != nil {
		t.Fatalf("BuildServerCard() error = %v", err)
	}

	r := httptest.NewRequest(http.MethodHead, "/server-card", nil)
	w := httptest.NewRecorder()
	Handler(card).ServeHTTP(w, r)
	res := w.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := res.Header.Get("ETag"); got == "" {
		t.Fatal("ETag is empty")
	}
	if body := w.Body.String(); body != "" {
		t.Fatalf("body = %q, want empty", body)
	}
}

func TestMountUsesDefaultPath(t *testing.T) {
	card, err := BuildServerCard(testImplementation(), WithName("com.example/dice"), WithDescription("Rolls dice."))
	if err != nil {
		t.Fatalf("BuildServerCard() error = %v", err)
	}
	mux := http.NewServeMux()
	Mount(mux, "", card)

	r := httptest.NewRequest(http.MethodGet, DefaultPath, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Result().StatusCode, http.StatusOK)
	}
}

func serveServerCard(t *testing.T, handler http.Handler, ifNoneMatch string) (int, http.Header, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/server-card", nil)
	if ifNoneMatch != "" {
		r.Header.Set("If-None-Match", ifNoneMatch)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	res := w.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return res.StatusCode, res.Header, body
}

func assertDiscoveryHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for key, want := range map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": http.MethodGet,
		"Access-Control-Allow-Headers": "Content-Type",
		"Cache-Control":                "public, max-age=3600",
	} {
		if got := h.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func quotedSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + fmt.Sprintf("%x", sum) + `"`
}
