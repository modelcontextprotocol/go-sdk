// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

			sseHandler := NewSSEHandler(func(*http.Request) *Server { return server }, nil)

			serverSessions := make(chan *ServerSession, 1)
			sseHandler.onConnection = func(ss *ServerSession) {
				select {
				case serverSessions <- ss:
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

			clientTransport := &SSEClientTransport{
				Endpoint:   httpServer.URL,
				HTTPClient: customClient,
			}

			c := NewClient(testImpl, nil)
			cs, err := c.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := cs.Ping(ctx, nil); err != nil {
				t.Fatal(err)
			}
			ss := <-serverSessions
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
			if diff := cmp.Diff(wantHi, gotHi, ctrCmpOpts...); diff != "" {
				t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
			}

			// Verify that customClient was used
			if atomic.LoadInt64(&customClientUsed) == 0 {
				t.Error("Expected custom HTTP client to be used, but it wasn't")
			}

			t.Run("badrequests", func(t *testing.T) {
				msgEndpoint := cs.mcpConn.(*sseClientConn).msgEndpoint.String()

				// Test some invalid data, and verify that we get 400s.
				badRequests := []struct {
					name             string
					body             string
					responseContains string
				}{
					{"not a method", `{"jsonrpc":"2.0", "method":"notamethod"}`, "not handled"},
					{"missing ID", `{"jsonrpc":"2.0", "method":"ping"}`, "missing id"},
				}
				for _, r := range badRequests {
					t.Run(r.name, func(t *testing.T) {
						resp, err := http.Post(msgEndpoint, "application/json", bytes.NewReader([]byte(r.body)))
						if err != nil {
							t.Fatal(err)
						}
						defer resp.Body.Close()
						if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
							t.Errorf("Sending bad request %q: got status %d, want %d", r.body, got, want)
						}
						result, err := io.ReadAll(resp.Body)
						if err != nil {
							t.Fatalf("Reading response: %v", err)
						}
						if !bytes.Contains(result, []byte(r.responseContains)) {
							t.Errorf("Response body does not contain %q:\n%s", r.responseContains, string(result))
						}
					})
				}
			})

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
