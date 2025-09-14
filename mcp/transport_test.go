// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"io"
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

func TestStdioTransport(t *testing.T) {
	tests := []struct {
		name     string
		setupIn  func() io.ReadCloser
		setupOut func() io.WriteCloser
		wantErr  bool
	}{
		{
			name:     "defaults_use_stdin_stdout",
			setupIn:  func() io.ReadCloser { return nil },
			setupOut: func() io.WriteCloser { return nil },
			wantErr:  false,
		},
		{
			name:     "custom_streams",
			setupIn:  func() io.ReadCloser { r, _ := io.Pipe(); return r },
			setupOut: func() io.WriteCloser { _, w := io.Pipe(); return w },
			wantErr:  false,
		},
		{
			name:     "partial_custom_in_only",
			setupIn:  func() io.ReadCloser { return io.NopCloser(strings.NewReader("")) },
			setupOut: func() io.WriteCloser { return nil },
			wantErr:  false,
		},
		{
			name:     "partial_custom_out_only",
			setupIn:  func() io.ReadCloser { return nil },
			setupOut: func() io.WriteCloser { _, w := io.Pipe(); return w },
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := &StdioTransport{
				In:  tt.setupIn(),
				Out: tt.setupOut(),
			}

			conn, err := transport.Connect(context.Background())
			if (err != nil) != tt.wantErr {
				t.Errorf("StdioTransport.Connect() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if conn == nil {
				t.Error("StdioTransport.Connect() returned nil connection")
				return
			}

			defer conn.Close()
		})
	}
}

func TestStdioTransportDefaults(t *testing.T) {
	transport := &StdioTransport{}

	if transport.In != nil {
		t.Error("StdioTransport{}.In should be nil (uses default)")
	}

	if transport.Out != nil {
		t.Error("StdioTransport{}.Out should be nil (uses default)")
	}

	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatalf("StdioTransport{}.Connect() failed: %v", err)
	}
	defer conn.Close()
}

func TestStdioTransportReadWrite(t *testing.T) {
	ctx := context.Background()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()

	transport := &StdioTransport{
		In:  r,
		Out: w,
	}

	conn, err := transport.Connect(ctx)
	if err != nil {
		t.Fatalf("StdioTransport.Connect() failed: %v", err)
	}
	defer conn.Close()

	// Test that we can write a message and it gets transmitted
	testMsg := &jsonrpc.Request{
		ID:     jsonrpc2.Int64ID(1),
		Method: "test",
		Params: nil,
	}

	// Write message in a goroutine since pipe may block
	go func() {
		if err := conn.Write(ctx, testMsg); err != nil {
			t.Errorf("conn.Write() failed: %v", err)
		}
	}()

	// Read the message back
	receivedMsg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("conn.Read() failed: %v", err)
	}

	if req, ok := receivedMsg.(*jsonrpc.Request); !ok || req.Method != "test" {
		t.Errorf("Expected request with method 'test', got %v", receivedMsg)
	}
}
