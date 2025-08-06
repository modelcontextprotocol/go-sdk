// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"io/fs"
	"sync"
)

// SessionState is the state of a session.
type SessionState struct {
	// InitializeParams are the parameters from the initialize request.
	InitializeParams *InitializeParams `json:"initializeParams"`

	// LogLevel is the logging level for the session.
	LogLevel LoggingLevel `json:"logLevel"`

	// TODO: resource subscriptions
}

// SessionStore is an interface for storing and retrieving session state.
type SessionStore interface {
	// Load retrieves the session state for the given session ID.
	// If there is none, it returns nil, fs.ErrNotExist.
	Load(ctx context.Context, sessionID string) (*SessionState, error)
	// Store saves the session state for the given session ID.
	Store(ctx context.Context, sessionID string, state *SessionState) error
	// Delete removes the session state for the given session ID.
	Delete(ctx context.Context, sessionID string) error
}

// MemorySessionStore is an in-memory implementation of SessionStore.
// It is safe for concurrent use.
type MemorySessionStore struct {
	mu    sync.Mutex
	store map[string]*SessionState
}

// NewMemorySessionStore creates a new MemorySessionStore.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		store: make(map[string]*SessionState),
	}
}

// Load retrieves the session state for the given session ID.
func (s *MemorySessionStore) Load(ctx context.Context, sessionID string) (*SessionState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.store[sessionID]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return state, nil
}

// Store saves the session state for the given session ID.
func (s *MemorySessionStore) Store(ctx context.Context, sessionID string, state *SessionState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[sessionID] = state
	return nil
}

// Delete removes the session state for the given session ID.
func (s *MemorySessionStore) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, sessionID)
	return nil
}
