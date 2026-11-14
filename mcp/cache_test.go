// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

// TestReadResourceCacheRetention verifies that ReadResource retains only the
// results the server marked as cacheable. A result with no TTL can never be
// served from the cache, so retaining it would grow the session by one result
// for every distinct URI read.
func TestReadResourceCacheRetention(t *testing.T) {
	var stableReads atomic.Int32
	srv := NewServer(testImpl, nil)
	srv.AddResourceTemplate(&ResourceTemplate{Name: "volatile", URITemplate: "test://volatile/{id}"},
		func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
			return &ReadResourceResult{
				Contents: []*ResourceContents{{URI: req.Params.URI, Text: "volatile"}},
			}, nil
		})
	srv.AddResourceTemplate(&ResourceTemplate{Name: "stable", URITemplate: "test://stable/{id}"},
		func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
			stableReads.Add(1)
			return &ReadResourceResult{
				Cacheable: Cacheable{TTLMs: 60_000},
				Contents:  []*ResourceContents{{URI: req.Params.URI, Text: "stable"}},
			}, nil
		})
	cs := mustConnect(t, srv, nil)

	for range 2 {
		for _, uri := range []string{"test://volatile/1", "test://volatile/2", "test://stable/1"} {
			if _, err := cs.ReadResource(t.Context(), &ReadResourceParams{URI: uri}); err != nil {
				t.Fatalf("ReadResource(%q) error = %v", uri, err)
			}
		}
	}

	if got := stableReads.Load(); got != 1 {
		t.Errorf("the cacheable resource was read from the server %d times, want 1", got)
	}
	cs.readResourceCache.mu.Lock()
	got := slices.Sorted(maps.Keys(cs.readResourceCache.cachedValues))
	cs.readResourceCache.mu.Unlock()
	if diff := cmp.Diff([]string{"test://stable/1"}, got); diff != "" {
		t.Errorf("cached URIs mismatch (-want +got):\n%s", diff)
	}
}

func TestMethodCachePutCacheable(t *testing.T) {
	result := func(ttlMs int) *ReadResourceResult {
		return &ReadResourceResult{Cacheable: Cacheable{TTLMs: ttlMs}}
	}

	t.Run("uncacheable result removes a cached one", func(t *testing.T) {
		var mc methodCache[*ReadResourceResult]
		mc.putCacheable("test://a", result(60_000))
		mc.putCacheable("test://a", result(0))
		if got := len(mc.cachedValues); got != 0 {
			t.Errorf("cache holds %d entries after an uncacheable result, want 0", got)
		}
	})

	t.Run("expired entries are removed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var mc methodCache[*ReadResourceResult]
			const n = 2 * minCacheSweep
			for i := range n {
				mc.putCacheable(fmt.Sprintf("test://old/%d", i), result(1000))
			}
			time.Sleep(2 * time.Second)
			for i := range 2 * n {
				mc.putCacheable(fmt.Sprintf("test://new/%d", i), result(60_000))
			}
			if got, want := len(mc.cachedValues), 2*n; got != want {
				t.Errorf("cache holds %d entries, want only the %d that have not expired", got, want)
			}
		})
	})
}
