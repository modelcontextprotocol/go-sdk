// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

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
		name  string
		input string
		want  string
		// protocolVersion is the version negotiated by 'initialize'; empty
		// means no session state was pushed to the connection.
		protocolVersion string
		// requested is the version the client asked for, which differs from
		// protocolVersion when the server counter-offered.
		requested string
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
		{
			// The client asked for a version the server does not negotiate, so
			// the connection must follow the server's counter-offer (which
			// forbids batching) rather than the client's request.
			name:            "batching at a counter-offered version",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "JSON-RPC batching is not supported in 2025-06-18 and later (request version: 2025-11-25)",
			requested:       protocolVersion20241105,
			protocolVersion: protocolVersion20251125,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader(tt.input)),
			})
			t.Cleanup(func() { tr.Close() })
			if tt.protocolVersion != "" {
				requested := tt.requested
				if requested == "" {
					requested = tt.protocolVersion
				}
				tr.sessionUpdated(ServerSessionState{
					InitializeParams: &InitializeParams{
						ProtocolVersion: requested,
					},
					NegotiatedProtocolVersion: tt.protocolVersion,
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

// bufWriteCloser is a concurrency-safe io.WriteCloser over a bytes.Buffer.
type bufWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *bufWriteCloser) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *bufWriteCloser) Close() error { return nil }

func (b *bufWriteCloser) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestIOConnRead_MalformedFrameRecovers verifies that a syntactically malformed
// frame does not end the session: the transport replies with a JSON-RPC parse
// error (-32700) and resynchronizes to the next newline-delimited frame, so the
// following valid request is still delivered. This matches JSON-RPC 2.0 and the
// behavior of other MCP server libraries; previously a single bad frame
// terminated the stdio session.
func TestIOConnRead_MalformedFrameRecovers(t *testing.T) {
	out := &bufWriteCloser{}
	tr := newIOConn(rwc{
		rc: io.NopCloser(strings.NewReader("{bad json\n" + `{"jsonrpc":"2.0","id":7,"method":"ping"}` + "\n")),
		wc: out,
	})
	t.Cleanup(func() { tr.Close() })

	// The next Read must skip the malformed frame and return the valid one.
	msg, err := tr.Read(context.Background())
	if err != nil {
		t.Fatalf("Read after malformed frame: error = %v, want nil (session must survive)", err)
	}
	req, ok := msg.(*jsonrpc.Request)
	if !ok {
		t.Fatalf("message type = %T, want *jsonrpc.Request", msg)
	}
	if req.Method != "ping" || req.ID != jsonrpc2.Int64ID(7) {
		t.Errorf("recovered request = {method:%q id:%v}, want {ping 7}", req.Method, req.ID.Raw())
	}

	// A -32700 parse-error response must have been written for the bad frame.
	if got := out.String(); !strings.Contains(got, "-32700") || !strings.Contains(got, "parse error") {
		t.Errorf("expected a -32700 parse-error response, wrote: %q", got)
	}
}

// TestIOConnRead_MalformedFrameEdgeCases covers consecutive malformed frames and
// malformed frames at end-of-stream (with and without a trailing newline). Each
// Read is guarded by a timeout so a regression that hangs the session is caught
// as a failure rather than hanging the suite.
func TestIOConnRead_MalformedFrameEdgeCases(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":9,"method":"ping"}`
	tests := []struct {
		name       string
		input      string
		wantMethod string // "" means expect io.EOF (no valid frame follows)
	}{
		{"two malformed then valid", "{bad1\nalso bad}\n" + ping + "\n", "ping"},
		{"malformed then EOF with newline", "{bad\n", ""},
		{"malformed then EOF without newline", "{bad", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &bufWriteCloser{}
			tr := newIOConn(rwc{rc: io.NopCloser(strings.NewReader(tt.input)), wc: out})
			t.Cleanup(func() { tr.Close() })

			type result struct {
				msg jsonrpc.Message
				err error
			}
			done := make(chan result, 1)
			go func() {
				m, e := tr.Read(context.Background())
				done <- result{m, e}
			}()

			select {
			case r := <-done:
				if tt.wantMethod == "" {
					if r.err != io.EOF {
						t.Errorf("err = %v, want io.EOF (session should end cleanly after the bad frame)", r.err)
					}
				} else {
					if r.err != nil {
						t.Fatalf("err = %v, want nil", r.err)
					}
					req, ok := r.msg.(*jsonrpc.Request)
					if !ok || req.Method != tt.wantMethod {
						t.Errorf("got %#v, want a %q request", r.msg, tt.wantMethod)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Read blocked: the session neither recovered nor terminated")
			}

			if !strings.Contains(out.String(), "-32700") {
				t.Errorf("expected a -32700 response for the malformed frame, wrote: %q", out.String())
			}
		})
	}
}
