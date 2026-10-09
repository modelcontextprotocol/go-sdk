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

// TestCancelFromPeerTellsWriter verifies that a writer that drops responses is
// told about a call the peer cancelled before the handler's context is
// cancelled, and not about a call cancelled locally. The response is written
// either way; dropping it is up to the writer. The barrier is a second call,
// which handlers answer only after the first one.
func TestCancelFromPeerTellsWriter(t *testing.T) {
	tests := []struct {
		name      string
		fromPeer  bool
		dropper   bool // whether the writer is a ResponseDropper
		want      []ID // the IDs of the responses written, in order
		wantDrops []ID // the IDs DropResponse is called with
	}{
		{"peer cancellation, plain writer", true, false, []ID{Int64ID(1), Int64ID(2)}, nil},
		{"peer cancellation, writer drops the response", true, true, []ID{Int64ID(1), Int64ID(2)}, []ID{Int64ID(1)}},
		{"local cancellation", false, true, []ID{Int64ID(1), Int64ID(2)}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := &recordingWriter{written: make(chan *Response, 2)}
			dropper := &dropperWriter{recordingWriter: rec}
			var writer Writer = rec
			if test.dropper {
				writer = dropper
			}
			started := make(chan struct{})
			dropsAtCancel := make(chan []ID, 1)
			conn, incoming := newCancelTestConn(t, writer, func(ctx context.Context) (any, error) {
				close(started)
				<-ctx.Done()
				dropsAtCancel <- dropper.drops()
				return nil, context.Cause(ctx)
			})

			incoming <- mustCall(t, 1, "slow")
			<-started
			if test.fromPeer {
				conn.CancelFromPeer(Int64ID(1), errPeerAskedToStop)
			} else {
				conn.Cancel(Int64ID(1))
			}
			incoming <- mustCall(t, 2, "barrier")

			var got []ID
			for range test.want {
				got = append(got, (<-rec.written).ID)
			}
			if !equalIDs(got, test.want) {
				t.Errorf("responses written for %v, want %v", got, test.want)
			}
			// The writer is told before the handler's context is cancelled.
			if drops := <-dropsAtCancel; !equalIDs(drops, test.wantDrops) {
				t.Errorf("when the handler's context was cancelled, DropResponse called for %v, want %v", drops, test.wantDrops)
			}
			if drops := dropper.drops(); !equalIDs(drops, test.wantDrops) {
				t.Errorf("DropResponse called for %v, want %v", drops, test.wantDrops)
			}

			if err := conn.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
	}
}

// TestCancelFromPeerHandlerIgnoresContext verifies that the writer is told of
// a peer cancellation as soon as it arrives, even when the handler ignores its
// context, that it still receives the response it drops once the handler
// returns, and that a cancellation arriving after that is ignored.
func TestCancelFromPeerHandlerIgnoresContext(t *testing.T) {
	rec := &recordingWriter{written: make(chan *Response, 2)}
	dropper := &dropperWriter{recordingWriter: rec}
	started := make(chan struct{})
	release := make(chan struct{})
	conn, incoming := newCancelTestConn(t, dropper, func(context.Context) (any, error) {
		close(started)
		<-release
		return struct{}{}, nil
	})

	incoming <- mustCall(t, 1, "slow")
	<-started
	conn.CancelFromPeer(Int64ID(1), errPeerAskedToStop)

	if drops := dropper.drops(); !equalIDs(drops, []ID{Int64ID(1)}) {
		t.Errorf("after the cancellation, DropResponse called for %v, want [1]", drops)
	}

	close(release)
	incoming <- mustCall(t, 2, "barrier")
	var got []ID
	for range 2 {
		got = append(got, (<-rec.written).ID)
	}
	if want := []ID{Int64ID(1), Int64ID(2)}; !equalIDs(got, want) {
		t.Errorf("responses written for %v, want %v", got, want)
	}
	conn.CancelFromPeer(Int64ID(1), errPeerAskedToStop)
	if drops := dropper.drops(); !equalIDs(drops, []ID{Int64ID(1)}) {
		t.Errorf("after a late cancellation, DropResponse called for %v, want [1]", drops)
	}

	if err := conn.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

// newCancelTestConn returns a Connection reading from the returned channel
// and writing to writer. Its handler answers "barrier" at once and runs slow
// for any other method. Handlers run one at a time, so once the barrier is
// answered, the slow call is finished.
func newCancelTestConn(t *testing.T, writer Writer, slow func(context.Context) (any, error)) (*Connection, chan Message) {
	t.Helper()
	incoming := make(chan Message, 2)
	handler := HandlerFunc(func(ctx context.Context, req *Request) (any, error) {
		if req.Method == "barrier" {
			return struct{}{}, nil
		}
		return slow(ctx)
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
	return conn, incoming
}

func mustCall(t *testing.T, id int64, method string) *Request {
	t.Helper()
	call, err := NewCall(Int64ID(id), method, nil)
	if err != nil {
		t.Fatal(err)
	}
	return call
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

// recordingWriter reports the responses a Connection writes.
type recordingWriter struct {
	written chan *Response
}

func (w *recordingWriter) Write(_ context.Context, msg Message) error {
	if resp, ok := msg.(*Response); ok {
		w.written <- resp
	}
	return nil
}

// dropperWriter is a recordingWriter that is also a [ResponseDropper],
// recording the IDs it is told about.
type dropperWriter struct {
	*recordingWriter

	mu      sync.Mutex
	dropped []ID
}

func (w *dropperWriter) DropResponse(id ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dropped = append(w.dropped, id)
}

func (w *dropperWriter) drops() []ID {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]ID(nil), w.dropped...)
}
