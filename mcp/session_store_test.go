// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"net/http/httptest"
	"testing"
)

func TestMemorySessionStorePersistence(t *testing.T) {
	store := NewMemoryServerSessionStore[*SSEServerTransport]()

	sessionID := "session-1"
	rr := httptest.NewRecorder()
	expectedSession := NewSSEServerTransport("endpoint-1", rr)
	store.Set(sessionID, expectedSession)

	actualSession, err := store.Get(sessionID)
	if err != nil {
		t.Error("unexpected session Get error", err)
	}
	if actualSession != expectedSession {
		t.Errorf("wanted %s to be %v but got %v", sessionID, expectedSession, actualSession)
	}

	err = store.Delete(sessionID)
	if err != nil {
		t.Error("unexpected session Delete error", err)
	}

	actualSession, err = store.Get(sessionID)
	if err != nil {
		t.Error("unexpected session Get error", err)
	}
	if actualSession != nil {
		t.Errorf("wanted %s to be nil but got %v", sessionID, actualSession)
	}
}
