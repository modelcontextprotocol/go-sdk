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
			name: "version hyphen range",
			impl: &mcp.Implementation{Name: "x", Version: "1.2.3 - 2.0.0"},
			opts: []BuildOption{WithName("com.example/hyphen-range"), WithDescription("desc")},
			want: "exact version",
		},
		{
			name: "version hyphen range with build metadata",
			impl: &mcp.Implementation{Name: "x", Version: "1.2.3+left - 2.0.0+right"},
			opts: []BuildOption{WithName("com.example/hyphen-range-build"), WithDescription("desc")},
			want: "exact version",
		},
		{
			name: "invalid name",
			impl: testImplementation(),
			opts: []BuildOption{WithName("no-slash"), WithDescription("desc")},
			want: "name",
		},
		{
			name: "invalid website URL",
			impl: &mcp.Implementation{Name: "x", Version: "1.0.0", WebsiteURL: "relative/path"},
			opts: []BuildOption{WithName("com.example/website"), WithDescription("desc")},
			want: "website URL",
		},
		{
			name: "website URL with unescaped space",
			impl: &mcp.Implementation{Name: "x", Version: "1.0.0", WebsiteURL: "https://example.com/foo bar"},
			opts: []BuildOption{WithName("com.example/website-space"), WithDescription("desc")},
			want: "website URL",
		},
		{
			name: "invalid icon source",
			impl: &mcp.Implementation{
				Name:    "x",
				Version: "1.0.0",
				Icons:   []mcp.Icon{{Source: "relative/path"}},
			},
			opts: []BuildOption{WithName("com.example/icon"), WithDescription("desc")},
			want: "icon",
		},
		{
			name: "invalid repository URL",
			impl: &mcp.Implementation{Name: "x", Version: "1.0.0"},
			opts: []BuildOption{
				WithName("com.example/repository"),
				WithDescription("desc"),
				WithRepository(Repository{URL: "relative/path", Source: "github"}),
			},
			want: "repository URL",
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
	for _, version := range []string{"1.0.0", "1.0.0-x", "1.0.0-X.1", "1.0.0-rc.x", "1.0.0+build.x", "2024-01-05", "release 2026"} {
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

func TestValidateCountsUnicodeCharacters(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		set   func(*ServerCard, string)
	}{
		{
			name:  "description",
			limit: 100,
			set:   func(card *ServerCard, value string) { card.Description = value },
		},
		{
			name:  "title",
			limit: 100,
			set:   func(card *ServerCard, value string) { card.Title = value },
		},
		{
			name:  "version",
			limit: 255,
			set:   func(card *ServerCard, value string) { card.Version = value },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card, err := BuildServerCard(testImplementation(), WithName("com.example/dice"), WithDescription("Rolls dice."))
			if err != nil {
				t.Fatalf("BuildServerCard() error = %v", err)
			}
			tt.set(card, strings.Repeat("界", tt.limit))
			if err := card.Validate(); err != nil {
				t.Fatalf("Validate() rejected %d Unicode characters: %v", tt.limit, err)
			}
			tt.set(card, strings.Repeat("界", tt.limit+1))
			if err := card.Validate(); err == nil {
				t.Fatalf("Validate() accepted %d Unicode characters, want error", tt.limit+1)
			}
		})
	}
}

func TestValidateNestedEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ServerCard)
		want   string
	}{
		{
			name: "icon theme",
			mutate: func(card *ServerCard) {
				card.Icons = []Icon{{Source: "https://example.com/icon.png", Theme: mcp.IconTheme("auto")}}
			},
			want: "theme",
		},
		{
			name: "remote variable format",
			mutate: func(card *ServerCard) {
				card.Remotes = []Remote{{
					Type:      RemoteTypeStreamableHTTP,
					URL:       "https://example.com/{tenant}/mcp",
					Variables: map[string]Input{"tenant": {Format: "date"}},
				}}
			},
			want: "input format",
		},
		{
			name: "header format",
			mutate: func(card *ServerCard) {
				card.Remotes = []Remote{{
					Type: RemoteTypeStreamableHTTP,
					URL:  "https://example.com/mcp",
					Headers: []KeyValueInput{{
						Name:  "Authorization",
						Input: Input{Format: "date"},
					}},
				}}
			},
			want: "input format",
		},
		{
			name: "header variable format",
			mutate: func(card *ServerCard) {
				card.Remotes = []Remote{{
					Type: RemoteTypeStreamableHTTP,
					URL:  "https://example.com/mcp",
					Headers: []KeyValueInput{{
						Name:      "Authorization",
						Input:     Input{Value: "Bearer {token}"},
						Variables: map[string]Input{"token": {Format: "date"}},
					}},
				}}
			},
			want: "input format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card, err := BuildServerCard(testImplementation(), WithName("com.example/dice"), WithDescription("Rolls dice."))
			if err != nil {
				t.Fatalf("BuildServerCard() error = %v", err)
			}
			tt.mutate(card)
			err = card.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestAbsoluteURIsAccepted(t *testing.T) {
	for _, uri := range []string{
		"https://example.com/a%20b",
		"data:image/png;base64,AAAA",
		"urn:air:example",
	} {
		t.Run(uri, func(t *testing.T) {
			if !isAbsoluteURI(uri) {
				t.Fatalf("isAbsoluteURI(%q) = false, want true", uri)
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

func TestHandlerDoesNotCacheErrors(t *testing.T) {
	card, err := BuildServerCard(testImplementation(), WithName("com.example/dice"), WithDescription("Rolls dice."))
	if err != nil {
		t.Fatalf("BuildServerCard() error = %v", err)
	}

	tests := []struct {
		name       string
		method     string
		card       *ServerCard
		wantStatus int
	}{
		{
			name:       "method not allowed",
			method:     http.MethodPost,
			card:       card,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "invalid card",
			method:     http.MethodGet,
			card:       &ServerCard{},
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/server-card", nil)
			w := httptest.NewRecorder()
			Handler(tt.card).ServeHTTP(w, r)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
			}
		})
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
