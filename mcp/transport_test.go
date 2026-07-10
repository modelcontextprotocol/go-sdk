// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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

// go-sdk#976: stdio must surface empty-method calls as requests, not responses.
func TestIOConnRead_EmptyMethod(t *testing.T) {
	tr := newIOConn(rwc{
		rc: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":5,"method":"","params":{}}`)),
	})
	t.Cleanup(func() { tr.Close() })

	msg, err := tr.Read(context.Background())
	if err != nil {
		t.Fatalf("ioConn.Read() error = %v", err)
	}
	req, ok := msg.(*jsonrpc.Request)
	if !ok {
		t.Fatalf("message type = %T, want *jsonrpc.Request", msg)
	}
	if req.Method != "" {
		t.Errorf("Method = %q, want empty string", req.Method)
	}
	if req.ID != jsonrpc2.Int64ID(5) {
		t.Errorf("ID = %v, want 5", req.ID.Raw())
	}
}

// When the peer closes the read side of the transport immediately after sending
// the last request, the server must still flush responses for requests it has
// already accepted.
func TestIOTransportFlushesOnReaderClose(t *testing.T) {
	c2sRead, c2sWrite := io.Pipe()
	s2cRead, s2cWrite := io.Pipe()

	server := NewServer(&Implementation{Name: "test", Version: "v1.0.0"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverExit := make(chan error, 1)
	go func() {
		serverExit <- server.Run(ctx, &IOTransport{Reader: c2sRead, Writer: s2cWrite})
	}()

	// Write initialize + tools/list back-to-back and immediately close the write side.
	const requests = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`
	go func() {
		if _, err := io.WriteString(c2sWrite, requests); err != nil {
			t.Errorf("write requests: %v", err)
		}
		c2sWrite.Close()
	}()

	// Read responses off the server's output pipe with a bounded deadline.
	// We expect two responses (ids 1 and 2). The notification does not
	// generate a response.
	type readResult struct {
		responses []map[string]any
		err       error
	}
	done := make(chan readResult, 1)
	go func() {
		var out []map[string]any
		scanner := bufio.NewScanner(s2cRead)
		for scanner.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				done <- readResult{out, err}
				return
			}
			// Skip server-initiated notifications; count only responses.
			if _, hasID := msg["id"]; hasID {
				out = append(out, msg)
				if len(out) >= 2 {
					break
				}
			}
		}
		done <- readResult{out, scanner.Err()}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("read responses: %v", r.err)
		}
		if len(r.responses) != 2 {
			t.Fatalf("got %d responses, want 2: %+v", len(r.responses), r.responses)
		}
		gotIDs := []any{r.responses[0]["id"], r.responses[1]["id"]}
		wantIDs := []any{float64(1), float64(2)}
		if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
			t.Errorf("response IDs mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for responses after reader close (issue #1061 regression)")
	}

	// Clean shutdown: cancel the server and drain its exit.
	cancel()
	s2cWrite.Close()
	s2cRead.Close()
	select {
	case <-serverExit:
	case <-time.After(2 * time.Second):
		t.Log("server did not exit within 2s after cancel")
	}
}
