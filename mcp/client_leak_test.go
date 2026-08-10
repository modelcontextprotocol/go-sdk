// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

// TestClientConnectClosesSessionOnUnsupportedVersion verifies that a failed
// Connect does not leak the ClientSession: when the server answers with an
// unsupported protocol version, the session's connection must be closed.
func TestClientConnectClosesSessionOnUnsupportedVersion(t *testing.T) {
	ctx := context.Background()

	s := NewServer(testImpl, nil)
	s.AddReceivingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			switch method {
			case methodDiscover:
				return nil, jsonrpc2.ErrMethodNotFound // force the legacy initialize path
			case methodInitialize:
				return &InitializeResult{ProtocolVersion: "1999-01-01", ServerInfo: testImpl}, nil
			}
			return next(ctx, method, req)
		}
	})

	ct, st := NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	defer ss.Close()

	rt := &closeRecordingTransport{Transport: ct}
	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, rt, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err == nil {
		_ = cs.Close()
		t.Fatal("Connect succeeded, want an unsupported protocol version error")
	}
	if !rt.closed.Load() {
		t.Error("Connect failed but did not close the session's connection (leak)")
	}
}

type closeRecordingTransport struct {
	Transport
	closed atomic.Bool
}

func (t *closeRecordingTransport) Connect(ctx context.Context) (Connection, error) {
	conn, err := t.Transport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &closeRecordingConn{Connection: conn, closed: &t.closed}, nil
}

type closeRecordingConn struct {
	Connection
	closed *atomic.Bool
}

func (c *closeRecordingConn) Close() error {
	c.closed.Store(true)
	return c.Connection.Close()
}
