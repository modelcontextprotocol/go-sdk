// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"maps"
	"sync"
	"time"
)

// methodCache is a per-method TTL cache for list and read results, as
// described in SEP-2549. Each entry is keyed by cursor (for paginated list
// methods) or URI (for resources/read).
type methodCache[R CacheableResult] struct {
	mu           sync.Mutex
	cachedValues map[string]*cacheEntry[R]
	// sweepAt is the number of entries at which putCacheable next removes
	// the expired ones.
	sweepAt int
}

type cacheEntry[R CacheableResult] struct {
	result     R
	receivedAt time.Time
}

func (e *cacheEntry[R]) isValid() bool {
	return time.Since(e.receivedAt) < time.Duration(e.result.GetTTLMs())*time.Millisecond
}

func (mc *methodCache[R]) get(key string) (R, bool) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	entry, ok := mc.cachedValues[key]
	if !ok {
		var zero R
		return zero, false
	}
	if entry.result.GetTTLMs() <= 0 || !entry.isValid() {
		delete(mc.cachedValues, key)
		var zero R
		return zero, false
	}
	return entry.result, true
}

// put stores result under key whatever its TTL, because lookupTool reads
// tools/list entries after they expire.
func (mc *methodCache[R]) put(key string, result R) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.cachedValues == nil {
		mc.cachedValues = make(map[string]*cacheEntry[R])
	}
	mc.cachedValues[key] = &cacheEntry[R]{
		result:     result,
		receivedAt: time.Now(),
	}
}

// minCacheSweep is the smallest cache size at which putCacheable looks for
// expired entries.
const minCacheSweep = 64

// putCacheable stores result under key if its TTL is positive, and removes
// the entry for key otherwise. Since get only removes an expired entry when
// its own key is looked up again, putCacheable also removes every expired
// entry each time the cache has doubled in size.
func (mc *methodCache[R]) putCacheable(key string, result R) {
	if result.GetTTLMs() <= 0 {
		mc.invalidateKey(key)
		return
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if mc.cachedValues == nil {
		mc.cachedValues = make(map[string]*cacheEntry[R])
	}
	if len(mc.cachedValues) >= mc.sweepAt {
		maps.DeleteFunc(mc.cachedValues, func(_ string, e *cacheEntry[R]) bool { return !e.isValid() })
		mc.sweepAt = max(2*len(mc.cachedValues), minCacheSweep)
	}
	mc.cachedValues[key] = &cacheEntry[R]{
		result:     result,
		receivedAt: time.Now(),
	}
}

func (mc *methodCache[R]) invalidate() {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	clear(mc.cachedValues)
}

func (mc *methodCache[R]) invalidateKey(key string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	delete(mc.cachedValues, key)
}

// cursorParams is the constraint for list-method params that carry a pagination
// cursor and can be checked for nil. Both methods are already implemented by
// every concrete list-params type.
type cursorParams interface {
	Params
	cursorPtr() *string
}

// cachedListResult returns a cached list result keyed by the request cursor
// (SEP-2549). It returns the zero value and false on miss or when params is nil.
func cachedListResult[P cursorParams, R CacheableResult](cache *methodCache[R], params P) (R, bool) {
	key := ""
	if !params.isNil() {
		if cp := params.cursorPtr(); cp != nil {
			key = *cp
		}
	}
	return cache.get(key)
}
