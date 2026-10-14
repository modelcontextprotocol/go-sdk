// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
)

// TestShuttingDownWrapsWriteError verifies that when a connection shuts down
// because its write side failed, the returned error preserves the underlying
// write error in its chain so callers can classify it with errors.Is (for
// example, distinguishing io.EOF from a clean host disconnect versus a real
// failure).
func TestShuttingDownWrapsWriteError(t *testing.T) {
	s := &inFlightState{writeErr: io.EOF}
	err := s.shuttingDown(ErrServerClosing)
	if !errors.Is(err, ErrServerClosing) {
		t.Errorf("shuttingDown() error = %v, want it to wrap ErrServerClosing", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("shuttingDown() error = %v, want it to wrap io.EOF", err)
	}
}

func TestShuttingDownWrapsReadError(t *testing.T) {
	s := &inFlightState{readErr: io.EOF}
	err := s.shuttingDown(ErrServerClosing)
	if !errors.Is(err, ErrServerClosing) {
		t.Errorf("shuttingDown() error = %v, want it to wrap ErrServerClosing", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("shuttingDown() error = %v, want it to wrap io.EOF", err)
	}
}

// TestCancelFromPeer verifies that CancelFromPeer tells a [ResponseDropper]
// writer about the call before the handler's context is cancelled, that a
// local Cancel and a cancellation arriving after the response do not, and that
// the response is written either way: dropping it is up to the writer.
func TestCancelFromPeer(t *testing.T) {
	for _, fromPeer := range []bool{true, false} {
		t.Run(fmt.Sprintf("fromPeer=%t", fromPeer), func(t *testing.T) {
			in := &chanReader{msgs: make(chan Message, 1)}
			w := &dropWriter{written: make(chan *Response, 1)}
			started := make(chan struct{})
			dropsAtCancel := make(chan []ID, 1)
			conn := NewConnection(context.Background(), ConnectionConfig{
				Reader: in,
				Writer: w,
				Closer: in,
				Bind: func(*Connection) Handler {
					return HandlerFunc(func(ctx context.Context, _ *Request) (any, error) {
						close(started)
						<-ctx.Done()
						dropsAtCancel <- w.drops()
						return nil, context.Cause(ctx)
					})
				},
			})

			call, err := NewCall(Int64ID(1), "slow", nil)
			if err != nil {
				t.Fatal(err)
			}
			in.msgs <- call
			<-started
			if fromPeer {
				conn.CancelFromPeer(Int64ID(1), errors.New("peer asked to stop"))
			} else {
				conn.Cancel(Int64ID(1))
			}

			var want []ID
			if fromPeer {
				want = []ID{Int64ID(1)}
			}
			if got := <-dropsAtCancel; !slices.Equal(got, want) {
				t.Errorf("when the handler's context was cancelled, DropResponse called for %v, want %v", got, want)
			}
			if resp := <-w.written; resp.ID != Int64ID(1) {
				t.Errorf("response written for %v, want 1", resp.ID)
			}
			conn.CancelFromPeer(Int64ID(1), nil) // the call is answered: nothing to drop
			if got := w.drops(); !slices.Equal(got, want) {
				t.Errorf("after a late cancellation, DropResponse called for %v, want %v", got, want)
			}
			if err := conn.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
	}
}

// chanReader is a Reader and Closer that delivers the messages sent on msgs,
// and reports io.EOF once closed.
type chanReader struct {
	msgs chan Message
	once sync.Once
}

func (r *chanReader) Read(ctx context.Context) (Message, error) {
	select {
	case msg, ok := <-r.msgs:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *chanReader) Close() error {
	r.once.Do(func() { close(r.msgs) })
	return nil
}

// dropWriter is a Writer and [ResponseDropper] that reports the responses it
// is given and records the calls it is told to drop.
type dropWriter struct {
	written chan *Response

	mu      sync.Mutex
	dropped []ID
}

func (w *dropWriter) Write(_ context.Context, msg Message) error {
	if resp, ok := msg.(*Response); ok {
		w.written <- resp
	}
	return nil
}

func (w *dropWriter) DropResponse(id ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dropped = append(w.dropped, id)
}

func (w *dropWriter) drops() []ID {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.dropped)
}
