// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

type cancellationReusePipe struct {
	incoming  chan jsonrpc2.Message
	closed    chan struct{}
	once      sync.Once
	entered   chan struct{}
	release   chan struct{}
	blockOnce sync.Once
	written   chan struct{}
}

func (p *cancellationReusePipe) Read(ctx context.Context) (jsonrpc2.Message, error) {
	select {
	case m := <-p.incoming:
		return m, nil
	case <-p.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *cancellationReusePipe) Write(ctx context.Context, m jsonrpc2.Message) error {
	p.blockOnce.Do(func() { close(p.entered); <-p.release })
	p.written <- struct{}{}
	return nil
}
func (p *cancellationReusePipe) Close() error { p.once.Do(func() { close(p.closed) }); return nil }

func TestCancellationBindsBeforeRequestIDReuse(t *testing.T) {
	// Arrange: an async old invocation and an independently tracked neighbor.
	p := &cancellationReusePipe{incoming: make(chan jsonrpc2.Message, 8), closed: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{}), written: make(chan struct{}, 8)}
	seen := make(chan context.Context, 8)
	oldFinish := make(chan struct{})
	newFinish := make(chan struct{})
	neighborFinish := make(chan struct{})
	c := jsonrpc2.NewConnection(context.Background(), jsonrpc2.ConnectionConfig{Reader: p, Writer: p, Closer: p, Bind: func(*jsonrpc2.Connection) jsonrpc2.Handler {
		return jsonrpc2.HandlerFunc(func(ctx context.Context, r *jsonrpc2.Request) (any, error) {
			jsonrpc2.Async(ctx)
			seen <- ctx
			switch r.Method {
			case "old":
				<-oldFinish
			case "new":
				<-newFinish
			case "neighbor":
				<-neighborFinish
			}
			return "complete", nil
		})
	}})
	defer c.Close()
	defer func() {
		for _, ch := range []chan struct{}{oldFinish, newFinish, neighborFinish, p.release} {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}()
	next := func() context.Context {
		t.Helper()
		select {
		case ctx := <-seen:
			return ctx
		case <-time.After(2 * time.Second):
			t.Fatal("handler missing")
			return nil
		}
	}
	id := jsonrpc2.Int64ID(7)
	p.incoming <- &jsonrpc2.Request{ID: id, Method: "old"}
	old := next()
	p.incoming <- &jsonrpc2.Request{ID: jsonrpc2.Int64ID(8), Method: "neighbor"}
	neighbor := next()
	// Act: actual MCP preempter must bind cancellation before returning.
	cancel := &canceller{conn: c, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := cancel.Preempt(context.Background(), &jsonrpc2.Request{Method: notificationCancelled, Params: json.RawMessage(`{"requestId":7,"reason":"selected call"}`)}); err != jsonrpc2.ErrNotHandled {
		t.Fatalf("Preempt() error = %v, want ErrNotHandled", err)
	}
	// Assert: immediate cancellation, with no scheduler wait and no neighbor cancellation.
	if old.Err() != context.Canceled {
		t.Fatal("preempter returned before selected invocation was cancelled")
	}
	if got := context.Cause(old); got == nil || got.Error() != "request cancelled by the peer: selected call" {
		t.Fatalf("old cancellation cause = %v", got)
	}
	if neighbor.Err() != nil {
		t.Fatal("neighbor cancelled")
	}
	// Arrange: old completion blocks after map deletion, new invocation reuses exact ID.
	close(oldFinish)
	select {
	case <-p.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("old response not blocked")
	}
	p.incoming <- &jsonrpc2.Request{ID: id, Method: "new"}
	newctx := next()
	// Act: finish old response after new invocation has registered.
	close(p.release)
	select {
	case <-p.written:
	case <-time.After(2 * time.Second):
		t.Fatal("old completion stuck")
	}
	// Assert: old completion did not cancel/delete new or neighbor state.
	if newctx.Err() != nil {
		t.Fatal("old completion cancelled reused invocation")
	}
	if _, err := cancel.Preempt(context.Background(), &jsonrpc2.Request{Method: notificationCancelled, Params: json.RawMessage(`{"requestId":7,"reason":"selected call"}`)}); err != jsonrpc2.ErrNotHandled {
		t.Fatalf("Preempt() error = %v, want ErrNotHandled", err)
	}
	if newctx.Err() != context.Canceled {
		t.Fatal("old completion deleted new cancellation entry")
	}
	if neighbor.Err() != nil {
		t.Fatal("neighbor cancelled after ID reuse")
	}
	close(newFinish)
	close(neighborFinish)
	for range 2 {
		select {
		case <-p.written:
		case <-time.After(2 * time.Second):
			t.Fatal("completion stuck")
		}
	}
}
