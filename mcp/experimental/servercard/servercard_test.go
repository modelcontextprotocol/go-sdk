// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package servercard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testImplementation() *mcp.Implementation {
	return &mcp.Implementation{
		Name:        "dice-roller",
		Title:       "Dice Roller",
		Description: "Rolls dice.",
		Version:     "1.0.0",
		WebsiteURL:  "https://example.com/dice",
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
			impl: &mcp.Implementation{Name: "x", Description: "desc"},
			opts: []BuildOption{WithName("com.example/no-version")},
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
			impl: &mcp.Implementation{Name: "x", Description: "desc", Version: ">=1.0.0"},
			opts: []BuildOption{WithName("com.example/range")},
			want: "exact version",
		},
		{
			name: "version wildcard",
			impl: &mcp.Implementation{Name: "x", Description: "desc", Version: "1.x"},
			opts: []BuildOption{WithName("com.example/wildcard")},
			want: "exact version",
		},
		{
			name: "invalid name",
			impl: testImplementation(),
			opts: []BuildOption{WithName("no-slash")},
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
			card, err := BuildServerCard(impl, WithName("com.example/dice"))
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
	for key, want := range map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": http.MethodGet,
		"Access-Control-Allow-Headers": "Content-Type",
		"Cache-Control":                "public, max-age=3600",
	} {
		if got := res.Header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	var got ServerCard
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Schema != SchemaURL || got.Name != card.Name || got.Remotes[0].URL != card.Remotes[0].URL {
		t.Fatalf("response card = %+v, want %+v", got, card)
	}
}

func TestMountUsesDefaultPath(t *testing.T) {
	card, err := BuildServerCard(testImplementation(), WithName("com.example/dice"))
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
