// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestSSEServerTransport_MaxBodyBytes(t *testing.T) {
	tpt := &SSEServerTransport{
		MaxBodyBytes: 16,
		incoming:     make(chan jsonrpc.Message, 1),
		done:         make(chan struct{}),
	}

	req := httptest.NewRequest(http.MethodPost, "http://example.invalid/session", bytes.NewReader(bytes.Repeat([]byte("a"), 17)))
	w := httptest.NewRecorder()
	tpt.ServeHTTP(w, req)

	resp := w.Result()
	resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusRequestEntityTooLarge; got != want {
		t.Fatalf("status code: got %d, want %d", got, want)
	}
}

func TestStreamableHTTPHandler_MaxBodyBytes(t *testing.T) {
	server := NewServer(testImpl, nil)

	tests := []struct {
		name      string
		stateless bool
	}{
		{name: "stateful", stateless: false},
		{name: "stateless", stateless: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewStreamableHTTPHandler(
				func(*http.Request) *Server { return server },
				&StreamableHTTPOptions{Stateless: tt.stateless, MaxBodyBytes: 16},
			)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			req, err := http.NewRequest(http.MethodPost, httpServer.URL, bytes.NewReader(bytes.Repeat([]byte("a"), 17)))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Accept", "application/json, text/event-stream")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			resp.Body.Close()
			if got, want := resp.StatusCode, http.StatusRequestEntityTooLarge; got != want {
				t.Fatalf("status code: got %d, want %d", got, want)
			}
		})
	}
}
