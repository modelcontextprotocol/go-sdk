// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"errors"
	"io"
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

// errPeerAskedToStop stands in for the reason an MCP "notifications/cancelled"
// carries, so the test exercises the signature a real peer cancellation uses
// rather than a nil cause no caller passes.
var errPeerAskedToStop = errors.New("peer asked to stop")

// TestCancelFromPeerSuppressesResponse verifies that a call the peer asked to
// cancel receives no response, while a call cancelled locally still does; the
// barrier is a second call, which handlers answer only after the first one.
func TestCancelFromPeerSuppressesResponse(t *testing.T) {
	tests := []struct {
		name     string
		fromPeer bool
		want     []ID // the IDs responded to, in order
	}{
		{"peer cancellation", true, []ID{Int64ID(2)}},
		{"local cancellation", false, []ID{Int64ID(1), Int64ID(2)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			incoming := make(chan Message, 2)
			writer := &recordingWriter{written: make(chan *Response, 2)}

			started := make(chan struct{})
			handler := HandlerFunc(func(ctx context.Context, req *Request) (any, error) {
				if req.Method == "barrier" {
					return struct{}{}, nil
				}
				close(started)
				<-ctx.Done()
				return nil, context.Cause(ctx)
			})

			conn := NewConnection(context.Background(), ConnectionConfig{
				Reader: &channelReader{messages: incoming},
				Writer: writer,
				Closer: &channelCloser{messages: incoming},
				Bind:   func(*Connection) Handler { return handler },
				OnDone: func() {},
				OnInternalError: func(err error) {
					t.Errorf("internal error: %v", err)
				},
			})

			slow, err := NewCall(Int64ID(1), "slow", nil)
			if err != nil {
				t.Fatal(err)
			}
			barrier, err := NewCall(Int64ID(2), "barrier", nil)
			if err != nil {
				t.Fatal(err)
			}

			incoming <- slow
			<-started
			if test.fromPeer {
				conn.CancelFromPeer(Int64ID(1), errPeerAskedToStop)
			} else {
				conn.Cancel(Int64ID(1))
			}
			incoming <- barrier

			var got []ID
			for range test.want {
				got = append(got, (<-writer.written).ID)
			}
			if !equalIDs(got, test.want) {
				t.Errorf("responded to %v, want %v", got, test.want)
			}

			var wantDropped []ID
			if test.fromPeer {
				wantDropped = []ID{Int64ID(1)}
			}
			if dropped := writer.dropped(); !equalIDs(dropped, wantDropped) {
				t.Errorf("dropped %v, want %v", dropped, wantDropped)
			}

			if err := conn.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
	}
}

// equalIDs reports whether two ID slices hold the same IDs in the same order.
func equalIDs(got, want []ID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Raw() != want[i].Raw() {
			return false
		}
	}
	return true
}

// channelReader delivers the messages sent to it, and reports io.EOF once the
// channel is closed.
type channelReader struct {
	messages chan Message
}

func (r *channelReader) Read(ctx context.Context) (Message, error) {
	select {
	case msg, ok := <-r.messages:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// channelCloser unblocks a [channelReader] reading from the same channel.
type channelCloser struct {
	once     sync.Once
	messages chan Message
}

func (c *channelCloser) Close() error {
	c.once.Do(func() { close(c.messages) })
	return nil
}

// recordingWriter reports the responses a Connection writes, and the calls it
// is told will get none.
type recordingWriter struct {
	written chan *Response

	mu   sync.Mutex
	drop []ID
}

func (w *recordingWriter) Write(_ context.Context, msg Message) error {
	if resp, ok := msg.(*Response); ok {
		w.written <- resp
	}
	return nil
}

// DropResponse implements [ResponseDropper].
func (w *recordingWriter) DropResponse(id ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.drop = append(w.drop, id)
}

func (w *recordingWriter) dropped() []ID {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]ID(nil), w.drop...)
}
