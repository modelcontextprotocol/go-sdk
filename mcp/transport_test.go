// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestBatchFraming(t *testing.T) {
	// This test checks that the ndjsonFramer can read and write JSON batches.
	//
	// The framer is configured to write a batch size of 2, and we confirm that
	// nothing is sent over the wire until the second write, at which point both
	// messages become available.
	ctx := context.Background()

	r, w := io.Pipe()
	tport := newIOConn(rwc{r, w})
	tport.outgoingBatch = make([]jsonrpc.Message, 0, 2)
	t.Cleanup(func() { tport.Close() })

	// Read the two messages into a channel, for easy testing later.
	read := make(chan jsonrpc.Message)
	go func() {
		for range 2 {
			msg, _ := tport.Read(ctx)
			read <- msg
		}
	}()

	// The first write should not yet be observed by the reader.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(1), Method: "test"})
	select {
	case got := <-read:
		t.Fatalf("after one write, got message %v", got)
	default:
	}

	// ...but the second write causes both messages to be observed.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(2), Method: "test"})
	for _, want := range []int64{1, 2} {
		got := <-read
		if got := got.(*jsonrpc.Request).ID.Raw(); got != want {
			t.Errorf("got message #%d, want #%d", got, want)
		}
	}
}

func TestIOConnRead(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		want            string
		protocolVersion string
	}{
		{
			name:  "valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}}`,
			want:  "",
		},
		{
			name: "newline at the end of first valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}}
			`,
			want: "",
		},
		{
			name:  "bad data at the end of first valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}},`,
			want:  "invalid trailing data at the end of stream",
		},
		{
			name:            "batching unknown protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "",
			protocolVersion: "",
		},
		{
			name:  "windows newline at the end of first valid json input",
			input: "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"test\",\"params\":{}}\r\n",
			want:  "",
		},
		{
			name:            "batching old protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "",
			protocolVersion: protocolVersion20241105,
		},
		{
			name:            "batching new protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "JSON-RPC batching is not supported in 2025-06-18 and later (request version: 2025-06-18)",
			protocolVersion: protocolVersion20250618,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader(tt.input)),
			})
			t.Cleanup(func() { tr.Close() })
			if tt.protocolVersion != "" {
				tr.sessionUpdated(ServerSessionState{
					InitializeParams: &InitializeParams{
						ProtocolVersion: tt.protocolVersion,
					},
				})
			}
			_, err := tr.Read(context.Background())
			if err == nil && tt.want != "" {
				t.Errorf("ioConn.Read() got nil error but wanted %v", tt.want)
			}
			if err != nil && err.Error() != tt.want {
				t.Errorf("ioConn.Read() = %v, want %v", err.Error(), tt.want)
			}
		})
	}
}

func TestScanEventsBufferError(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name                  string
		clientTransport       func(url string) Transport
		serverHandler         func(server *Server) http.Handler
		responseLength        int
		expectedContainsError string
	}{
		{
			name: "sse-large-output",
			clientTransport: func(url string) Transport {
				return &SSEClientTransport{
					Endpoint:    url,
					MaxLineSize: 1024,
				}
			},
			serverHandler: func(server *Server) http.Handler {
				return NewSSEHandler(func(req *http.Request) *Server { return server }, nil)
			},
			responseLength:        10000,
			expectedContainsError: "exceeded max line length",
		},
		{
			name: "streamable-large-output",
			clientTransport: func(url string) Transport {
				return &StreamableClientTransport{
					Endpoint:    url,
					MaxLineSize: 1024,
				}
			},
			serverHandler: func(server *Server) http.Handler {
				return NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
			},
			responseLength:        10000,
			expectedContainsError: "exceeded max line length",
		},
		{
			name: "sse-small-output",
			clientTransport: func(url string) Transport {
				return &SSEClientTransport{
					Endpoint:    url,
					MaxLineSize: 1024,
				}
			},
			serverHandler: func(server *Server) http.Handler {
				return NewSSEHandler(func(req *http.Request) *Server { return server }, nil)
			},
			responseLength: 512,
		},
		{
			name: "streamable-small-output",
			clientTransport: func(url string) Transport {
				return &StreamableClientTransport{
					Endpoint:    url,
					MaxLineSize: 1024,
				}
			},
			serverHandler: func(server *Server) http.Handler {
				return NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
			},
			responseLength: 512,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			largeResponse := strings.Repeat("x", tt.responseLength)
			server := NewServer(testImpl, nil)
			AddTool(server, &Tool{Name: "largeTool", Description: "returns large response"}, func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
				return &CallToolResult{Content: []Content{&TextContent{Text: largeResponse}}}, nil, nil
			})

			httpHandler := tt.serverHandler(server)
			httpServer := httptest.NewServer(mustNotPanic(t, httpHandler))
			defer httpServer.Close()

			client := NewClient(testImpl, nil)
			clientTransport := tt.clientTransport(httpServer.URL)
			session, err := client.Connect(ctx, clientTransport, nil)
			if err != nil {
				t.Fatalf("client.Connect() failed: %v", err)
			}
			defer session.Close()

			_, err = session.CallTool(ctx, &CallToolParams{
				Name:      "largeTool",
				Arguments: map[string]any{},
			})
			if tt.expectedContainsError != "" {
				if tt.expectedContainsError != "" && err == nil {
					t.Fatal("expected error due to small buffer, got nil")
				}

				if !strings.Contains(err.Error(), "exceeded max line length") {
					t.Fatalf("expected buffer-related error, got: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("client.CallTool() unexpectedly failed: %v", err)
				}
			}

		})
	}
}
