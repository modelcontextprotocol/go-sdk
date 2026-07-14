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

// The peer writes three JSON-RPC frames and immediately closes its side of
// the connection. The server must still flush responses for the two calls it
// has already accepted (initialize and tools/list) before shutting down.
//
// The test uses an [IOTransport] wired to two [io.Pipe] pairs so we can
// independently close the client→server direction (to trigger io.EOF on the
// server's read side) while keeping the server→client direction open (so we
// can still read the drained responses). NewInMemoryTransports is not usable
// here because it's built on net.Pipe, whose Close is bidirectional and
// therefore cannot express a half-closed stdin.
func TestServerDrainsResponsesOnReaderEOF(t *testing.T) {
	// Two unidirectional pipes emulate a stdin/stdout pair.
	clientToServerR, clientToServerW := io.Pipe() // server reads, test writes
	serverToClientR, serverToClientW := io.Pipe() // test reads, server writes

	server := NewServer(&Implementation{Name: "test", Version: "v1.0.0"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ss, err := server.Connect(ctx, &IOTransport{
		Reader: clientToServerR,
		Writer: serverToClientW,
	}, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	const frames = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`
	if _, err := io.WriteString(clientToServerW, frames); err != nil {
		t.Fatalf("write frames: %v", err)
	}
	// Trigger the "stdin closed" condition on the server side while leaving
	// the response pipe (serverToClient) open.
	if err := clientToServerW.Close(); err != nil {
		t.Fatalf("close client-to-server: %v", err)
	}

	// Bound the read with a timeout so a regression surfaces as a clear
	// failure rather than a hung test.
	deadline := time.AfterFunc(5*time.Second, func() { _ = serverToClientR.Close() })
	defer deadline.Stop()

	var responses []map[string]any
	scanner := bufio.NewScanner(serverToClientR)
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			t.Fatalf("decode response: %v: %s", err, scanner.Bytes())
		}
		if _, hasID := msg["id"]; hasID {
			responses = append(responses, msg)
		}
		if len(responses) >= 2 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning responses: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("got %d responses, want 2 (issue #1061 regression): %+v", len(responses), responses)
	}
	gotIDs := []any{responses[0]["id"], responses[1]["id"]}
	wantIDs := []any{float64(1), float64(2)}
	if diff := cmp.Diff(wantIDs, gotIDs); diff != "" {
		t.Errorf("response IDs mismatch (-want +got):\n%s", diff)
	}
	// Both responses must carry real results, not context-canceled errors,
	// which would indicate in-flight handlers were cancelled by EOF before
	// they could complete.
	for _, r := range responses {
		if r["error"] != nil {
			t.Errorf("response id=%v carries error (handler cancelled by EOF): %v", r["id"], r["error"])
		}
	}
}
