// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeReader struct {
	msgs chan Message
}

func (r fakeReader) Read(ctx context.Context) (Message, error) {
	select {
	case m := <-r.msgs:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type fakeWriter struct{ out chan Message }

func (w fakeWriter) Write(_ context.Context, m Message) error {
	w.out <- m
	return nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// TestUnmarshalableResultSendsError verifies that a result that can't be
// marshaled is reported to the log and answered with a generic error response.
func TestUnmarshalableResultSendsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	call, err := NewCall(Int64ID(7), "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := fakeReader{msgs: make(chan Message, 1)}
	r.msgs <- call
	w := fakeWriter{out: make(chan Message, 1)}
	internalErrs := make(chan error, 1)
	NewConnection(ctx, ConnectionConfig{
		Reader: r,
		Writer: w,
		Closer: nopCloser{},
		Bind: func(*Connection) Handler {
			return HandlerFunc(func(context.Context, *Request) (any, error) {
				return math.Inf(1), nil
			})
		},
		OnInternalError: func(err error) { internalErrs <- err },
	})

	resp, ok := (<-w.out).(*Response)
	if !ok {
		t.Fatal("want a Response")
	}
	if resp.ID != Int64ID(7) {
		t.Errorf("response ID = %v, want 7", resp.ID)
	}
	var werr *WireError
	if !errors.As(resp.Error, &werr) || werr.Code != -32603 || !strings.Contains(resp.Error.Error(), "malformed result") {
		t.Errorf("response error = %v, want generic -32603 error", resp.Error)
	}
	if err := <-internalErrs; err == nil {
		t.Error("want internal error to be logged")
	}
}

// TestShuttingDownWrapsWriteError verifies that when a connection shuts down
// because its write side failed, the returned error preserves the underlying
// write error in its chain so callers can classify it with errors.Is (for
// example, distinguishing io.EOF from a clean host disconnect versus a real
// failure).
func TestShuttingDownWrapsWriteError(t *testing.T) {
	s := &inFlightState{writeErr: io.EOF}
	err := s.shuttingDown(ErrConnClosing)
	if !errors.Is(err, ErrConnClosing) {
		t.Errorf("shuttingDown() error = %v, want it to wrap ErrConnClosing", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("shuttingDown() error = %v, want it to wrap io.EOF", err)
	}
}

func TestShuttingDownWrapsReadError(t *testing.T) {
	s := &inFlightState{readErr: io.EOF}
	err := s.shuttingDown(ErrConnClosing)
	if !errors.Is(err, ErrConnClosing) {
		t.Errorf("shuttingDown() error = %v, want it to wrap ErrConnClosing", err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("shuttingDown() error = %v, want it to wrap io.EOF", err)
	}
}

func TestReadErrorRetiresCalls(t *testing.T) {
	for _, readErr := range []error{io.EOF, io.ErrUnexpectedEOF, errors.New("read failed")} {
		t.Run(readErr.Error(), func(t *testing.T) {
			ctx := context.Background()
			failed := make(chan struct{})
			fail := sync.OnceFunc(func() { close(failed) })
			w := fakeWriter{out: make(chan Message, 1)}
			conn := NewConnection(ctx, ConnectionConfig{
				Reader: readerFunc(func(context.Context) (Message, error) {
					<-failed
					return nil, readErr
				}),
				Writer: w,
				Closer: nopCloser{},
				Bind: func(*Connection) Handler {
					return HandlerFunc(func(context.Context, *Request) (any, error) {
						return nil, ErrNotHandled
					})
				},
			})
			t.Cleanup(func() {
				fail()
				conn.Close()
			})

			call := conn.Call(ctx, "test", nil)
			<-w.out
			fail()
			err := call.Await(ctx, nil)
			for _, want := range []error{ErrConnClosing, readErr} {
				if !errors.Is(err, want) {
					t.Errorf("Await() = %v, want an error wrapping %v", err, want)
				}
			}
		})
	}
}

type readerFunc func(context.Context) (Message, error)

func (f readerFunc) Read(ctx context.Context) (Message, error) { return f(ctx) }

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

// TestDuplicateInFlightRequestID verifies that a call reusing an in-flight ID
// receives an ErrInvalidRequest response with its ID omitted, without running
// the handler again or disturbing the original in-flight call's tracking.
func TestDuplicateInFlightRequestID(t *testing.T) {
	for _, cancelOrig := range []bool{false, true} {
		name := "original completes"
		if cancelOrig {
			name = "original stays cancellable"
		}
		t.Run(name, func(t *testing.T) {
			in := &chanReader{msgs: make(chan Message, 4)}
			w := fakeWriter{out: make(chan Message, 4)}
			var calls atomic.Int32
			started := make(chan struct{})
			release := make(chan struct{})

			conn := NewConnection(context.Background(), ConnectionConfig{
				Reader: in,
				Writer: w,
				Closer: in,
				Bind: func(*Connection) Handler {
					return HandlerFunc(func(ctx context.Context, req *Request) (any, error) {
						Async(ctx)
						if req.Method == "slow" {
							if calls.Add(1) == 1 {
								close(started)
							}
							select {
							case <-release:
								return "done", nil
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
						return "ok", nil
					})
				},
			})

			call1, err := NewCall(Int64ID(5), "slow", nil)
			if err != nil {
				t.Fatal(err)
			}
			call2, err := NewCall(Int64ID(5), "slow", nil)
			if err != nil {
				t.Fatal(err)
			}
			in.msgs <- call1
			<-started

			// Reusing id 5 while call1 is in flight must return -32600 with no id.
			in.msgs <- call2
			dupResp, ok := (<-w.out).(*Response)
			if !ok {
				t.Fatal("want *Response for duplicate ID")
			}
			if dupResp.ID.IsValid() {
				t.Errorf("duplicate-ID response ID = %v, want omitted", dupResp.ID.Raw())
			}
			if !errors.Is(dupResp.Error, ErrInvalidRequest) {
				t.Errorf("duplicate-ID error = %v, want ErrInvalidRequest", dupResp.Error)
			}
			if strings.Contains(dupResp.Error.Error(), "{5}") {
				t.Errorf("duplicate-ID error = %q, want raw ID without struct braces", dupResp.Error)
			}
			wire, err := EncodeMessage(dupResp)
			if err != nil {
				t.Fatalf("EncodeMessage: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatalf("Unmarshal(%s): %v", wire, err)
			}
			if _, hasID := fields["id"]; hasID {
				t.Errorf("encoded duplicate-ID response must omit id, got %s", wire)
			}

			// Original call must remain unaffected and still tracked in incomingByID.
			if cancelOrig {
				conn.Cancel(Int64ID(5))
			} else {
				close(release)
			}
			origResp, ok := (<-w.out).(*Response)
			if !ok {
				t.Fatal("want *Response for original call")
			}
			if origResp.ID != Int64ID(5) {
				t.Errorf("original response ID = %v, want 5", origResp.ID.Raw())
			}
			if cancelOrig {
				if !errors.Is(origResp.Error, context.Canceled) {
					t.Errorf("cancelled original error = %v, want context.Canceled", origResp.Error)
				}
			} else if origResp.Error != nil {
				t.Errorf("original response error = %v, want nil", origResp.Error)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("slow handler called %d times, want 1", got)
			}

			// Reusing id 5 sequentially after call1 finishes must succeed.
			call3, err := NewCall(Int64ID(5), "fast", nil)
			if err != nil {
				t.Fatal(err)
			}
			in.msgs <- call3
			reuseResp, ok := (<-w.out).(*Response)
			if !ok {
				t.Fatal("want *Response for sequential reuse")
			}
			if reuseResp.ID != Int64ID(5) || reuseResp.Error != nil {
				t.Errorf("sequential reuse response = %+v, want id=5 with nil error", reuseResp)
			}

			if err := conn.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
	}
}
