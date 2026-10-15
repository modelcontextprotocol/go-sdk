// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"math"
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
	if !errors.As(resp.Error, &werr) || werr.Code != -32603 || werr.Message != "internal error: result could not be encoded" {
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
