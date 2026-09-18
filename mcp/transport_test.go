// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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

// TestIOConnReadBatchNotifications is a regression test for go-sdk#1256:
// notifications carried inside a JSON-RPC batch must not be tracked as awaiting
// a response. Only calls belong in a batch's response array, so a batch's
// notifications must neither keep it from completing nor pollute the
// per-connection duplicate-ID tracking.
func TestIOConnReadBatchNotifications(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		// input is one or more newline-separated batch payloads.
		input string
		// wantCalls records, for each message Read returns in order, whether it
		// is a call (true) or a notification (false). Reading every message
		// must succeed.
		wantCalls []bool
		// wantTrackedCalls is the number of in-flight call requests tracked across
		// batches awaiting a response once every message has been read, before any
		// response is written.
		wantTrackedCalls int
		// respondIDs are the call IDs to respond to, in order.
		respondIDs []int64
		// wantWrites are the batch response payloads expected on the wire, each
		// matched as a substring.
		wantWrites []string
	}{
		{
			name:             "call and notification is answered",
			input:            `[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]`,
			wantCalls:        []bool{true, false},
			wantTrackedCalls: 1,
			respondIDs:       []int64{2},
			wantWrites:       []string{`[{"jsonrpc":"2.0","id":2,"result":{}}]`},
		},
		{
			name:             "only notifications keeps the connection open",
			input:            `[{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":98}},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]`,
			wantCalls:        []bool{false, false},
			wantTrackedCalls: 0,
		},
		{
			name: "repeated batches holding a notification",
			input: `[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]` + "\n" +
				`[{"jsonrpc":"2.0","id":3,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}]`,
			wantCalls:        []bool{true, false, true, false},
			wantTrackedCalls: 2,
			respondIDs:       []int64{2, 3},
			wantWrites: []string{
				`[{"jsonrpc":"2.0","id":2,"result":{}}]`,
				`[{"jsonrpc":"2.0","id":3,"result":{}}]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink := new(bytes.Buffer)
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader(tt.input)),
				wc: nopCloserWriter{sink},
			})
			t.Cleanup(func() { tr.Close() })

			for i, wantCall := range tt.wantCalls {
				msg, err := tr.Read(ctx)
				if err != nil {
					t.Fatalf("Read() #%d: unexpected error: %v", i, err)
				}
				req, ok := msg.(*jsonrpc.Request)
				if !ok {
					t.Fatalf("Read() #%d = %T, want *jsonrpc.Request", i, msg)
				}
				if req.IsCall() != wantCall {
					t.Fatalf("Read() #%d IsCall() = %t, want %t", i, req.IsCall(), wantCall)
				}
			}

			// Only calls are tracked as awaiting a response; notifications must
			// not linger in the batch bookkeeping.
			tr.batchMu.Lock()
			gotTrackedCalls := len(tr.batches)
			tr.batchMu.Unlock()
			if gotTrackedCalls != tt.wantTrackedCalls {
				t.Errorf("tracked calls = %d, want %d", gotTrackedCalls, tt.wantTrackedCalls)
			}

			for _, id := range tt.respondIDs {
				resp := &jsonrpc.Response{ID: jsonrpc2.Int64ID(id), Result: []byte("{}")}
				if err := tr.Write(ctx, resp); err != nil {
					t.Fatalf("Write(response id=%d): %v", id, err)
				}
			}

			got := sink.String()
			for _, want := range tt.wantWrites {
				if !strings.Contains(got, want) {
					t.Errorf("batch responses = %q, missing %q", got, want)
				}
			}
		})
	}
}

// TestIOConnFrameCap verifies single inbound frame cap enforcement.
func TestIOConnFrameCap(t *testing.T) {
	tests := []struct {
		name             string
		r                io.ReadCloser
		limit            int
		wantMessageCount int
		wantErr          bool
	}{
		{
			name:    "infinite string",
			r:       &endlessReader{prefix: `"`, repeat: "A"},
			limit:   1024,
			wantErr: true,
		},
		{
			name:    "cap does not reset per new line",
			r:       &endlessReader{prefix: "[", repeat: "0,\n"},
			limit:   1024,
			wantErr: true,
		},
		{
			name:  "cap resets per message",
			limit: 1024, // < 600 * 3
			r: io.NopCloser(strings.NewReader(func() string {
				var b strings.Builder
				for i := range 3 {
					fmt.Fprintf(&b, `{"jsonrpc":"2.0","id":%d,"method":"test","params":{"pad":"`, i)
					b.WriteString(strings.Repeat("A", 600))
					b.WriteString(`"}}`)
					b.WriteByte('\n')
				}
				return b.String()
			}())),
			wantMessageCount: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConnLimited(rwc{rc: tt.r}, tt.limit)
			t.Cleanup(func() { tr.Close() })

			read := func() (jsonrpc.Message, error) {
				type result struct {
					msg jsonrpc.Message
					err error
				}
				done := make(chan result, 1)

				go func() {
					msg, err := tr.Read(context.Background())
					done <- result{msg, err}
				}()

				select {
				case r := <-done:
					return r.msg, r.err
				case <-time.After(10 * time.Second):
					t.Fatal("Read() did not return: frame cap failed to trip")
					return nil, nil
				}
			}

			for i := range tt.wantMessageCount {
				msg, err := read()
				if err != nil {
					t.Fatalf("Read() #%d error = %v, want nil", i, err)
				}
				if msg == nil {
					t.Fatalf("Read() #%d returned nil message", i)
				}
			}
			if tt.wantErr {
				if _, err := read(); !errors.Is(err, errFrameTooLarge) {
					t.Fatalf("Read() error = %v, want errFrameTooLarge", err)
				}
			}
		})
	}
}
