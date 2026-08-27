// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReadResourceCacheHint(t *testing.T) {
	handler := func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
		return &ReadResourceResult{
			Contents: []*ResourceContents{{URI: req.Params.URI, Text: "data"}},
		}, nil
	}

	tests := []struct {
		name           string
		uri            string
		wantTTLMs      int
		wantCacheScope string
	}{
		{
			name:           "resource private hint",
			uri:            "test://private",
			wantTTLMs:      5000,
			wantCacheScope: "private",
		},
		{
			name:           "resource public ttl-only hint",
			uri:            "test://public",
			wantTTLMs:      1000,
			wantCacheScope: "public",
		},
		{
			name:           "resource without hint uses server default",
			uri:            "test://none",
			wantTTLMs:      0,
			wantCacheScope: "public",
		},
		{
			name:           "template hint applies to matched uri",
			uri:            "tmpl://tenant/42",
			wantTTLMs:      2000,
			wantCacheScope: "private",
		},
	}

	ctx := context.Background()
	srv := NewServer(testImpl, nil)
	srv.AddResource(&Resource{URI: "test://private", Name: "private", CacheHint: &CacheHint{TTLMs: 5000, CacheScope: "private"}}, handler)
	srv.AddResource(&Resource{URI: "test://public", Name: "public", CacheHint: &CacheHint{TTLMs: 1000}}, handler)
	srv.AddResource(&Resource{URI: "test://none", Name: "none"}, handler)
	srv.AddResourceTemplate(&ResourceTemplate{URITemplate: "tmpl://tenant/{id}", Name: "tenant", CacheHint: &CacheHint{TTLMs: 2000, CacheScope: "private"}}, handler)

	conn := mustConnect(t, srv, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := conn.ReadResource(ctx, &ReadResourceParams{URI: tt.uri})
			if err != nil {
				t.Fatalf("ReadResource(%q) error = %v", tt.uri, err)
			}
			if res.TTLMs != tt.wantTTLMs {
				t.Errorf("TTLMs = %d, want %d", res.TTLMs, tt.wantTTLMs)
			}
			if res.CacheScope != tt.wantCacheScope {
				t.Errorf("CacheScope = %q, want %q", res.CacheScope, tt.wantCacheScope)
			}
		})
	}
}

func TestReadResourceHandlerOverridesCacheHint(t *testing.T) {
	handler := func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
		return &ReadResourceResult{
			Cacheable: Cacheable{TTLMs: 9000, CacheScope: "public"},
			Contents:  []*ResourceContents{{URI: req.Params.URI, Text: "data"}},
		}, nil
	}

	ctx := context.Background()
	srv := NewServer(testImpl, nil)
	srv.AddResource(&Resource{URI: "test://override", Name: "override", CacheHint: &CacheHint{TTLMs: 5000, CacheScope: "private"}}, handler)

	conn := mustConnect(t, srv, nil)

	res, err := conn.ReadResource(ctx, &ReadResourceParams{URI: "test://override"})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if res.TTLMs != 9000 {
		t.Errorf("TTLMs = %d, want 9000 (handler value should win over hint)", res.TTLMs)
	}
	if res.CacheScope != "public" {
		t.Errorf("CacheScope = %q, want \"public\" (handler value should win over hint)", res.CacheScope)
	}
}

func TestCacheHintNotSerializedInListings(t *testing.T) {
	r := &Resource{URI: "file:///a", Name: "a", CacheHint: &CacheHint{TTLMs: 5000, CacheScope: "private"}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal(Resource) error = %v", err)
	}
	if got := string(b); containsAny(got, "cacheHint", "CacheHint", "ttlMs", "cacheScope") {
		t.Errorf("Resource JSON unexpectedly contains cache hint fields: %s", got)
	}

	rt := &ResourceTemplate{URITemplate: "file:///{x}", Name: "t", CacheHint: &CacheHint{TTLMs: 1000}}
	b2, err := json.Marshal(rt)
	if err != nil {
		t.Fatalf("Marshal(ResourceTemplate) error = %v", err)
	}
	if got := string(b2); containsAny(got, "cacheHint", "CacheHint", "ttlMs", "cacheScope") {
		t.Errorf("ResourceTemplate JSON unexpectedly contains cache hint fields: %s", got)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
