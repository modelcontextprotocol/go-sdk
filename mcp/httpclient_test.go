// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckTransportRedirect(t *testing.T) {
	base, err := url.Parse("http://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	newReq := func(raw string, method string) *http.Request {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{Method: method, URL: u}
	}

	tests := []struct {
		name    string
		req     *http.Request
		via     []*http.Request
		wantErr bool
	}{
		{
			name: "same origin path change allowed",
			req:  newReq("http://example.com/other", http.MethodPost),
			via:  []*http.Request{newReq("http://example.com/mcp", http.MethodPost)},
		},
		{
			name:    "cross origin blocked",
			req:     newReq("http://evil.example/mcp", http.MethodPost),
			via:     []*http.Request{newReq("http://example.com/mcp", http.MethodPost)},
			wantErr: true,
		},
		{
			name:    "method change blocked",
			req:     newReq("http://example.com/mcp", http.MethodGet),
			via:     []*http.Request{newReq("http://example.com/mcp", http.MethodPost)},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkTransportRedirect(base, tc.req, tc.via)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkTransportRedirect() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestTransportHTTPClientRespectsUserCheckRedirect(t *testing.T) {
	base, err := url.Parse("http://example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	var userCalled atomic.Bool
	client := transportHTTPClient(&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			userCalled.Store(true)
			return nil
		},
	}, base)
	req, _ := http.NewRequest(http.MethodGet, "http://other.example/", nil)
	if err := client.CheckRedirect(req, nil); err != nil {
		t.Fatalf("CheckRedirect() = %v, want nil", err)
	}
	if !userCalled.Load() {
		t.Fatal("user CheckRedirect was not called")
	}
}

func TestStreamableClientTransportBlocksCrossOriginRedirect(t *testing.T) {
	ctx := context.Background()
	received := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		received <- struct{}{}
		http.Error(w, "redirect target", http.StatusInternalServerError)
	}))
	t.Cleanup(target.Close)

	server := NewServer(testImpl, nil)
	handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"ping"`)) {
			http.Redirect(w, req, target.URL, http.StatusTemporaryRedirect)
			return
		}
		handler.ServeHTTP(w, req)
	}))
	t.Cleanup(origin.Close)

	session, err := NewClient(testImpl, nil).Connect(ctx, &StreamableClientTransport{Endpoint: origin.URL}, nil)
	if err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer session.Close()

	if err := session.Ping(ctx, nil); err == nil {
		t.Fatal("Ping() succeeded, want redirect error")
	}

	select {
	case <-received:
		t.Fatal("cross-origin redirect was followed")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestStreamableClientTransportAllowsSameOriginRedirect(t *testing.T) {
	ctx := context.Background()
	server := NewServer(testImpl, nil)
	handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.Handle("/mcp2", handler)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/mcp" && req.Method == http.MethodPost {
			http.Redirect(w, req, "/mcp2", http.StatusTemporaryRedirect)
			return
		}
		mux.ServeHTTP(w, req)
	}))
	t.Cleanup(httpServer.Close)

	session, err := NewClient(testImpl, nil).Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer session.Close()

	if err := session.Ping(ctx, nil); err != nil {
		t.Fatalf("Ping() after same-origin redirect: %v", err)
	}
}

func TestSSEClientTransportBlocksCrossOriginRedirect(t *testing.T) {
	ctx := context.Background()
	received := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		received <- struct{}{}
		http.Error(w, "redirect target", http.StatusInternalServerError)
	}))
	t.Cleanup(target.Close)

	server := NewServer(testImpl, nil)
	sseHandler := NewSSEHandler(func(*http.Request) *Server { return server }, nil)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			body, err := io.ReadAll(req.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
			if bytes.Contains(body, []byte(`"method":"ping"`)) {
				http.Redirect(w, req, target.URL, http.StatusTemporaryRedirect)
				return
			}
		}
		sseHandler.ServeHTTP(w, req)
	}))
	t.Cleanup(origin.Close)

	session, err := NewClient(testImpl, nil).Connect(ctx, &SSEClientTransport{Endpoint: origin.URL}, nil)
	if err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer session.Close()

	if err := session.Ping(ctx, nil); err == nil {
		t.Fatal("Ping() succeeded, want redirect error")
	}

	select {
	case <-received:
		t.Fatal("cross-origin redirect was followed")
	case <-time.After(250 * time.Millisecond):
	}
}
