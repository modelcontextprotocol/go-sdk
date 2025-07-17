// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSSEServer(t *testing.T) {
	for _, closeServerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("closeServerFirst=%t", closeServerFirst), func(t *testing.T) {
			ctx := context.Background()
			server := NewServer(testImpl, nil)
			AddTool(server, &Tool{Name: "greet"}, sayHi)

			sseHandler := NewSSEHandler(func(*http.Request) *Server { return server })

			conns := make(chan *ServerSession, 1)
			sseHandler.onConnection = func(cc *ServerSession) {
				select {
				case conns <- cc:
				default:
				}
			}
			httpServer := httptest.NewServer(sseHandler)
			defer httpServer.Close()

			var customClientUsed int64
			customClient := &http.Client{
				Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					atomic.AddInt64(&customClientUsed, 1)
					return http.DefaultTransport.RoundTrip(req)
				}),
			}

			clientTransport := NewSSEClientTransport(httpServer.URL, &SSEClientTransportOptions{
				HTTPClient: customClient,
			})

			c := NewClient(testImpl, nil)
			cs, err := c.Connect(ctx, clientTransport)
			if err != nil {
				t.Fatal(err)
			}
			if err := cs.Ping(ctx, nil); err != nil {
				t.Fatal(err)
			}
			ss := <-conns
			gotHi, err := cs.CallTool(ctx, &CallToolParams{
				Name:      "greet",
				Arguments: map[string]any{"Name": "user"},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantHi := &CallToolResult{
				Content: []Content{
					&TextContent{Text: "hi user"},
				},
			}
			if diff := cmp.Diff(wantHi, gotHi); diff != "" {
				t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
			}

			// Verify that customClient was used
			if atomic.LoadInt64(&customClientUsed) == 0 {
				t.Error("Expected custom HTTP client to be used, but it wasn't")
			}

			// Test that closing either end of the connection terminates the other
			// end.
			if closeServerFirst {
				cs.Close()
				ss.Wait()
			} else {
				ss.Close()
				cs.Wait()
			}
		})
	}
}

// roundTripperFunc is a helper to create a custom RoundTripper
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
