// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func TestReadEOFAllowsInFlightResponseToDrain(t *testing.T) {
	call, err := NewCall(Int64ID(1), "ping", nil)
	if err != nil {
		t.Fatal(err)
	}

	reader := &scriptedReader{messages: []Message{call}}
	writer := &recordingWriter{}

	conn := NewConnection(context.Background(), ConnectionConfig{
		Reader: reader,
		Writer: writer,
		Closer: nopCloser{},
		Bind: func(conn *Connection) Handler {
			return HandlerFunc(func(context.Context, *Request) (any, error) {
				waitForReadErr(t, conn)
				return "pong", nil
			})
		},
		OnInternalError: func(err error) {
			t.Errorf("internal error: %v", err)
		},
	})

	waitForConnection(t, conn)

	responses := writer.messages()
	if len(responses) != 1 {
		t.Fatalf("writer saw %d messages, want 1", len(responses))
	}
	response, ok := responses[0].(*Response)
	if !ok {
		t.Fatalf("writer saw %T, want *Response", responses[0])
	}
	if response.ID != Int64ID(1) {
		t.Fatalf("response ID = %v, want 1", response.ID.Raw())
	}
	if string(response.Result) != `"pong"` {
		t.Fatalf("response result = %s, want %q", response.Result, `"pong"`)
	}
}

type scriptedReader struct {
	messages []Message
	index    int
}

func (r *scriptedReader) Read(context.Context) (Message, error) {
	if r.index >= len(r.messages) {
		return nil, io.EOF
	}
	msg := r.messages[r.index]
	r.index++
	return msg, nil
}

type recordingWriter struct {
	mu      sync.Mutex
	written []Message
}

func (w *recordingWriter) Write(_ context.Context, msg Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written = append(w.written, msg)
	return nil
}

func (w *recordingWriter) messages() []Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Message(nil), w.written...)
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func waitForReadErr(t *testing.T, conn *Connection) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		conn.stateMu.Lock()
		err := conn.state.readErr
		conn.stateMu.Unlock()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("read error = %v, want EOF", err)
			}
			return
		}

		select {
		case <-deadline:
			t.Fatal("timed out waiting for read EOF")
		case <-ticker.C:
		}
	}
}

func waitForConnection(t *testing.T, conn *Connection) {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		done <- conn.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wait returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connection shutdown")
	}
}
