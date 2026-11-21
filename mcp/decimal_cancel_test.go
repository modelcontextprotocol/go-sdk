// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

func TestCancelDecimalIntegerID(t *testing.T) {
	for _, id := range []string{"9007199254740993.0", "90071992547409930e-1"} {
		t.Run(id, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, b := net.Pipe()
			transport, peer := newIOConn(a), newIOConn(b)
			started := make(chan struct{})
			canceled := make(chan struct{})
			preempt := &canceller{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			conn := jsonrpc2.NewConnection(ctx, jsonrpc2.ConnectionConfig{
				Reader: transport, Writer: transport, Closer: transport, Preempter: preempt,
				Bind: func(c *jsonrpc2.Connection) jsonrpc2.Handler {
					preempt.conn = c
					return jsonrpc2.HandlerFunc(func(callCtx context.Context, req *jsonrpc2.Request) (any, error) {
						if req.Method != "wait" {
							return nil, jsonrpc2.ErrNotHandled
						}
						close(started)
						select {
						case <-callCtx.Done():
							close(canceled)
							return nil, callCtx.Err()
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					})
				},
			})
			t.Cleanup(func() { cancel(); peer.Close(); conn.Close() })
			if err := peer.Write(ctx, &jsonrpc2.Request{ID: jsonrpc2.Int64ID(9007199254740993), Method: "wait"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := peer.Write(ctx, &jsonrpc2.Request{Method: notificationCancelled, Params: json.RawMessage(`{"requestId":` + id + `}`)}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("decimal requestId did not cancel the matching integer request")
			}
			if _, err := peer.Read(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
