// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestCancellationPreservesAdjacentLargeRequestIDs(t *testing.T) {
	reader := newCancellationTestReader()
	writer := &cancellationTestWriter{messages: make(chan jsonrpc.Message, 4)}
	started := make(chan int64, 2)
	cancelled := make(chan int64, 2)
	release := make(chan struct{})
	preempter := &canceller{}

	conn := jsonrpc2.NewConnection(context.Background(), jsonrpc2.ConnectionConfig{
		Reader:    reader,
		Writer:    writer,
		Closer:    reader,
		Preempter: preempter,
		Bind: func(*jsonrpc2.Connection) jsonrpc2.Handler {
			return jsonrpc2.HandlerFunc(func(ctx context.Context, req *jsonrpc.Request) (any, error) {
				if !req.IsCall() {
					return nil, nil
				}
				jsonrpc2.Async(ctx)
				id := req.ID.Raw().(int64)
				started <- id
				select {
				case <-ctx.Done():
					cancelled <- id
					return nil, ctx.Err()
				case <-release:
					return map[string]any{"id": id}, nil
				}
			})
		},
	})
	preempter.conn = conn

	for _, wire := range []string{
		`{"jsonrpc":"2.0","id":9007199254740992,"method":"slow"}`,
		`{"jsonrpc":"2.0","id":9007199254740993,"method":"slow"}`,
	} {
		reader.messages <- mustDecodeCancellationMessage(t, wire)
	}
	waitForIDs(t, started, 9007199254740992, 9007199254740993)

	reader.messages <- mustDecodeCancellationMessage(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"_meta":{"trace":"kept"},"reason":"test","requestId":9007199254740993}}`)
	select {
	case got := <-cancelled:
		if got != 9007199254740993 {
			t.Fatalf("cancelled ID = %d, want 9007199254740993", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
	select {
	case got := <-cancelled:
		t.Fatalf("unexpected cancellation of ID %d", got)
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationRejectsInvalidRequestIDs(t *testing.T) {
	preempter := &canceller{}
	for _, requestID := range []string{`1.5`, `9223372036854775808`, `true`, `{}`} {
		t.Run(requestID, func(t *testing.T) {
			req := mustDecodeCancellationMessage(t, fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s}}`, requestID)).(*jsonrpc.Request)
			if _, err := preempter.Preempt(context.Background(), req); err == nil {
				t.Fatal("Preempt() succeeded, want invalid request ID error")
			}
		})
	}
}

func TestDecodeCancelledRequestID(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  any
	}{
		{name: "missing", value: ``, want: nil},
		{name: "null", value: `null`, want: nil},
		{name: "string", value: `"9007199254740993"`, want: "9007199254740993"},
		{name: "zero", value: `0`, want: int64(0)},
		{name: "negative", value: `-17`, want: int64(-17)},
		{name: "large adjacent low", value: `9007199254740992`, want: int64(9007199254740992)},
		{name: "large adjacent high", value: `9007199254740993`, want: int64(9007199254740993)},
		{name: "maximum", value: `9223372036854775807`, want: int64(9223372036854775807)},
		{name: "minimum", value: `-9223372036854775808`, want: int64(-9223372036854775808)},
		{name: "integral exponent", value: `900719925474099300e-2`, want: int64(9007199254740993)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := `{"_meta":{"trace":"kept"},"reason":"test"}`
			if test.value != "" {
				params = `{"_meta":{"trace":"kept"},"reason":"test","requestId":` + test.value + `}`
			}
			id, err := decodeCancelledRequestID([]byte(params))
			if err != nil {
				t.Fatal(err)
			}
			if id.Raw() != test.want {
				t.Fatalf("ID = %#v, want %#v", id.Raw(), test.want)
			}
		})
	}
}

type cancellationTestReader struct {
	messages chan jsonrpc.Message
	done     chan struct{}
	once     sync.Once
}

func newCancellationTestReader() *cancellationTestReader {
	return &cancellationTestReader{messages: make(chan jsonrpc.Message, 4), done: make(chan struct{})}
}

func (r *cancellationTestReader) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case msg := <-r.messages:
		return msg, nil
	case <-r.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *cancellationTestReader) Close() error {
	r.once.Do(func() { close(r.done) })
	return nil
}

type cancellationTestWriter struct {
	messages chan jsonrpc.Message
}

func (w *cancellationTestWriter) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case w.messages <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mustDecodeCancellationMessage(t *testing.T, wire string) jsonrpc.Message {
	t.Helper()
	msg, err := jsonrpc.DecodeMessage([]byte(wire))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func waitForIDs(t *testing.T, ids <-chan int64, want ...int64) {
	t.Helper()
	got := make(map[int64]bool)
	for range want {
		select {
		case id := <-ids:
			got[id] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for calls to start")
		}
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("request %d did not start", id)
		}
	}
}
