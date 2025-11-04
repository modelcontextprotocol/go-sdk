// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ServerSessionStateStore persists server session state across process
// restarts.
//
// Implementations must be safe for concurrent use.
type ServerSessionStateStore interface {
	// Load returns the previously saved state for sessionID. A nil result
	// indicates that no state is available.
	Load(ctx context.Context, sessionID string) (*ServerSessionState, error)
	// Save persists the provided state. The state must not be modified after the
	// call returns. Passing a nil state is equivalent to Delete.
	Save(ctx context.Context, sessionID string, state *ServerSessionState) error
	// Delete forgets any state associated with sessionID. This method must not
	// return an error if no state is recorded.
	Delete(ctx context.Context, sessionID string) error
}

// MemoryServerSessionStateStore is an in-memory implementation of
// ServerSessionStateStore.
//
// It is primarily intended for testing or simple deployments.
type MemoryServerSessionStateStore struct {
	mu     sync.RWMutex
	states map[string][]byte
}

// NewMemoryServerSessionStateStore returns a MemoryServerSessionStateStore.
func NewMemoryServerSessionStateStore() *MemoryServerSessionStateStore {
	return &MemoryServerSessionStateStore{
		states: make(map[string][]byte),
	}
}

// Load implements ServerSessionStateStore.
func (s *MemoryServerSessionStateStore) Load(ctx context.Context, sessionID string) (*ServerSessionState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	data, ok := s.states[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	var state ServerSessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode server session state: %w", err)
	}
	return &state, nil
}

// Save implements ServerSessionStateStore.
func (s *MemoryServerSessionStateStore) Save(ctx context.Context, sessionID string, state *ServerSessionState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == nil {
		return s.Delete(ctx, sessionID)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode server session state: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[sessionID] = data
	return nil
}

// Delete implements ServerSessionStateStore.
func (s *MemoryServerSessionStateStore) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.states, sessionID)
	s.mu.Unlock()
	return nil
}
