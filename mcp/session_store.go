// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"iter"
	"sync"
)

// ServerSessionStore is a store of [Transport] sessions.
//
// The store must be thread-safe.
type ServerSessionStore[T Transport] interface {
	Get(id string) (T, error)
	Set(id string, session T) error
	Delete(id string) error
	Reset() error
	All() (iter.Seq[T], error)
}

// MemoryServerSessionStore is a simple in-memory implementation of
// [ServerSessionStore].
type MemoryServerSessionStore[T Transport] struct {
	mu       sync.Mutex
	sessions map[string]T
}

func NewMemoryServerSessionStore[T Transport]() *MemoryServerSessionStore[T] {
	return &MemoryServerSessionStore[T]{
		sessions: make(map[string]T),
	}
}

func (s *MemoryServerSessionStore[T]) Get(id string) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id], nil
}

func (s *MemoryServerSessionStore[T]) Set(id string, session T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = session
	return nil
}

func (s *MemoryServerSessionStore[T]) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

func (s *MemoryServerSessionStore[T]) All() (iter.Seq[T], error) {
	return func(yield func(T) bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, session := range s.sessions {
			if !yield(session) {
				return
			}
		}
	}, nil
}

func (s *MemoryServerSessionStore[T]) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]T)
	return nil
}
