// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_ExportErrSessionMissing(t *testing.T) {
	ctx := context.Background()

	// 1. Setup server
	impl := &Implementation{Name: "test", Version: "1.0.0"}
	server := NewServer(impl, nil)
	handler := NewStreamableHTTPHandler(func(r *http.Request) *Server { return server }, nil)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// 2. Setup client
	clientTransport := &StreamableClientTransport{
		Endpoint: ts.URL,
	}
	client := NewClient(impl, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer session.Close()

	// 3. Manually invalidate session on server
	handler.mu.Lock()
	if len(handler.sessions) != 1 {
		handler.mu.Unlock()
		t.Fatalf("expected 1 session, got %d", len(handler.sessions))
	}
	for id := range handler.sessions {
		delete(handler.sessions, id)
	}
	handler.mu.Unlock()

	// 4. Try to call a tool (or any request)
	_, err = session.ListTools(ctx, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// 5. Verify it's ErrSessionMissing
	if !errors.Is(err, ErrSessionMissing) {
		t.Errorf("expected error to wrap ErrSessionMissing, got: %v", err)
	}
}
