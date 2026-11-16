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

// maxCacheEntries is the most entries putCacheable keeps in a cache, since the
// number of distinct keys, such as resource URIs, is up to the caller and the
// server.
const maxCacheEntries = 512

// putCacheable stores result under key if its TTL is positive, and removes
// the entry for key otherwise. To add a key to a full cache, it first removes
// the expired entries and, if none have expired, the oldest one.
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
	if _, ok := mc.cachedValues[key]; !ok && len(mc.cachedValues) >= maxCacheEntries {
		maps.DeleteFunc(mc.cachedValues, func(_ string, e *cacheEntry[R]) bool { return !e.isValid() })
		if len(mc.cachedValues) >= maxCacheEntries {
			mc.deleteOldest()
		}
	}
	mc.cachedValues[key] = &cacheEntry[R]{
		result:     result,
		receivedAt: time.Now(),
	}
}

// deleteOldest removes the entry received first. mc.mu must be held.
func (mc *methodCache[R]) deleteOldest() {
	var oldestKey string
	var oldest *cacheEntry[R]
	for key, e := range mc.cachedValues {
		if oldest == nil || e.receivedAt.Before(oldest.receivedAt) {
			oldestKey, oldest = key, e
		}
	}
	delete(mc.cachedValues, oldestKey)
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
