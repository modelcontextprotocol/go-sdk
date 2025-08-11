// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"testing"
)

func TestMemorySessionStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySessionStore()

	sessionID := "test-session"
	state := &SessionState{LogLevel: "debug"}

	// Test Store and Load
	if err := store.Store(ctx, sessionID, state); err != nil {
		t.Fatalf("Store() error = %v", err)
	}

	loadedState, err := store.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loadedState == nil {
		t.Fatal("Load() returned nil state")
	}
	if loadedState.LogLevel != state.LogLevel {
		t.Errorf("Load() LogLevel = %v, want %v", loadedState.LogLevel, state.LogLevel)
	}

	// Test Delete
	if err := store.Delete(ctx, sessionID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	deletedState, err := store.Load(ctx, sessionID)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Load() after Delete(): got %v, want ErrNoSession", err)
	}
	if deletedState != nil {
		t.Error("Load() after Delete() returned non-nil state")
	}
}
