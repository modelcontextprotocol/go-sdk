// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
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

// TestResponseWaitsForPrecedingNotifications verifies that when notifications
// arrive on the wire ahead of a call's response, all preceding notification
// handlers complete before AsyncCall.Await returns (go-sdk#1337).
func TestResponseWaitsForPrecedingNotifications(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const numNotifications = 5
	r := fakeReader{msgs: make(chan Message, numNotifications+1)}
	w := fakeWriter{out: make(chan Message, 1)}

	var handled atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	conn := NewConnection(ctx, ConnectionConfig{
		Reader: r,
		Writer: w,
		Closer: nopCloser{},
		Bind: func(*Connection) Handler {
			return HandlerFunc(func(_ context.Context, req *Request) (any, error) {
				if handled.Add(1) == 1 {
					close(firstStarted)
					<-releaseFirst
				}
				return nil, nil
			})
		},
	})

	ac := conn.Call(ctx, "work", nil)
	sent := (<-w.out).(*Request)

	for range numNotifications {
		n, err := NewNotification("notify", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.msgs <- n
	}
	resp, err := NewResponse(sent.ID, "ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.msgs <- resp

	// Wait until the first notification handler is running and blocking handleAsync,
	// then allow it and all remaining notifications to complete.
	<-firstStarted
	close(releaseFirst)

	var got string
	if err := ac.Await(ctx, &got); err != nil {
		t.Fatalf("Await failed: %v", err)
	}
	if got != "ok" {
		t.Errorf("Await result = %q, want %q", got, "ok")
	}
	if n := handled.Load(); n != numNotifications {
		t.Errorf("handled %d notifications before Await returned, want %d", n, numNotifications)
	}
}

// TestSynchronousHandlerNestedCall verifies that a synchronous handler (one
// that does not call Async) can make an outgoing Call and await its response
// without deadlocking on its own handlerQueue slot, whether it passes the
// handler context or a background context.
func TestSynchronousHandlerNestedCall(t *testing.T) {
	for _, useHandlerCtx := range []bool{true, false} {
		name := "handler_ctx"
		if !useHandlerCtx {
			name = "background_ctx"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			r := fakeReader{msgs: make(chan Message, 4)}
			w := fakeWriter{out: make(chan Message, 4)}
			done := make(chan string, 1)

			NewConnection(ctx, ConnectionConfig{
				Reader: r,
				Writer: w,
				Closer: nopCloser{},
				Bind: func(c *Connection) Handler {
					return HandlerFunc(func(hctx context.Context, req *Request) (any, error) {
						if req.Method == "trigger" {
							callCtx := hctx
							if !useHandlerCtx {
								callCtx = context.Background()
							}
							var res string
							if err := c.Call(callCtx, "nested", nil).Await(callCtx, &res); err != nil {
								t.Errorf("nested Call failed: %v", err)
							}
							done <- res
						}
						return nil, nil
					})
				},
			})

			trigger, err := NewNotification("trigger", nil)
			if err != nil {
				t.Fatal(err)
			}
			trailing, err := NewNotification("trailing", nil)
			if err != nil {
				t.Fatal(err)
			}
			r.msgs <- trigger
			r.msgs <- trailing

			nestedReq := (<-w.out).(*Request)
			nestedResp, err := NewResponse(nestedReq.ID, "nested-ok", nil)
			if err != nil {
				t.Fatal(err)
			}
			r.msgs <- nestedResp

			if got := <-done; got != "nested-ok" {
				t.Errorf("nested call result = %q, want %q", got, "nested-ok")
			}
		})
	}
}

// TestRetireQueuedResponse verifies that Connection.Retire retires an AsyncCall
// even after its response has been read from the wire and queued behind an
// in-flight notification handler, without double-retiring when the queue drains.
func TestRetireQueuedResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := fakeReader{msgs: make(chan Message)}
	w := fakeWriter{out: make(chan Message, 4)}
	notifyStarted := make(chan struct{})
	releaseNotify := make(chan struct{})
	allHandled := make(chan struct{})
	var count atomic.Int32

	conn := NewConnection(ctx, ConnectionConfig{
		Reader: r,
		Writer: w,
		Closer: nopCloser{},
		Bind: func(*Connection) Handler {
			return HandlerFunc(func(_ context.Context, req *Request) (any, error) {
				if req.Method == "notify" {
					if count.Add(1) == 1 {
						close(notifyStarted)
						<-releaseNotify
					} else {
						close(allHandled)
					}
				}
				return nil, nil
			})
		},
	})

	ac := conn.Call(ctx, "work", nil)
	sent := (<-w.out).(*Request)

	n, err := NewNotification("notify", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.msgs <- n
	<-notifyStarted

	resp, err := NewResponse(sent.ID, "ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.msgs <- resp
	// Because r.msgs is unbuffered, sending a second notification only returns
	// after readIncoming has finished enqueuing resp into handlerQueue and
	// advanced to the next Read call.
	r.msgs <- n

	retireErr := errors.New("caller cancelled")
	conn.Retire(ac, retireErr)

	var got string
	if err := ac.Await(ctx, &got); !errors.Is(err, retireErr) {
		t.Fatalf("Await error = %v, want %v", err, retireErr)
	}

	close(releaseNotify)
	<-allHandled
}
