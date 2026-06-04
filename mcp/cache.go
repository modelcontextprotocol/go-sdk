// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
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

func (mc *methodCache[R]) forEach(f func(R)) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	for _, entry := range mc.cachedValues {
		f(entry.result)
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
