// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

type searchParams struct {
	ParamsBase
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type searchResult struct {
	ResultBase
	Hits  []string `json:"hits"`
	Total int      `json:"total"`
}

// callCustom calls a custom JSON-RPC method via the raw jsonrpc2 connection,
// bypassing the SDK's typed method dispatch.
func callCustom(ctx context.Context, conn *jsonrpc2.Connection, method string, params, result any) error {
	return conn.Call(ctx, method, params).Await(ctx, result)
}

func TestAddReceivingCustomMethod(t *testing.T) {
	ctx := context.Background()
	s := NewServer(testImpl, nil)

	AddReceivingCustomMethod(s, "acme/search", func(ctx context.Context, ss *ServerSession, params *searchParams) (*searchResult, error) {
		hits := []string{"result for " + params.Query}
		return &searchResult{
			Hits:  hits,
			Total: len(hits),
		}, nil
	})

	ct, st := NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	var result searchResult
	if err := callCustom(ctx, cs.getConn(), "acme/search", &searchParams{Query: "hello", Limit: 10}, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Hits) != 1 || result.Hits[0] != "result for hello" {
		t.Errorf("unexpected hits: %v", result.Hits)
	}
	if result.Total != 1 {
		t.Errorf("unexpected total: %d", result.Total)
	}
}

func TestCustomMethodGoesThoughMiddleware(t *testing.T) {
	ctx := context.Background()
	s := NewServer(testImpl, nil)

	var middlewareCalled bool
	s.AddReceivingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method == "acme/ping" {
				middlewareCalled = true
			}
			return next(ctx, method, req)
		}
	})

	type pingParams struct {
		ParamsBase
	}
	type pingResult struct {
		ResultBase
		Pong bool `json:"pong"`
	}
	AddReceivingCustomMethod(s, "acme/ping", func(ctx context.Context, ss *ServerSession, params *pingParams) (*pingResult, error) {
		return &pingResult{Pong: true}, nil
	})

	ct, st := NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	var result pingResult
	if err := callCustom(ctx, cs.getConn(), "acme/ping", &pingParams{}, &result); err != nil {
		t.Fatal(err)
	}

	if !result.Pong {
		t.Error("expected Pong to be true")
	}
	if !middlewareCalled {
		t.Error("middleware was not called for custom method")
	}
}

func TestCustomMethodNotOnOtherServers(t *testing.T) {
	ctx := context.Background()

	type emptyParams struct{ ParamsBase }
	type emptyResult struct{ ResultBase }

	// Server 1 has the custom method.
	s1 := NewServer(testImpl, nil)
	AddReceivingCustomMethod(s1, "acme/custom", func(ctx context.Context, ss *ServerSession, params *emptyParams) (*emptyResult, error) {
		return &emptyResult{}, nil
	})

	// Server 2 does NOT have the custom method.
	s2 := NewServer(testImpl, nil)

	// Test s1: custom method should work.
	ct1, st1 := NewInMemoryTransports()
	ss1, err := s1.Connect(ctx, st1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss1.Close() })

	c1 := NewClient(testImpl, nil)
	cs1, err := c1.Connect(ctx, ct1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs1.Close() })

	if err := callCustom(ctx, cs1.getConn(), "acme/custom", &emptyParams{}, &emptyResult{}); err != nil {
		t.Fatalf("custom method on s1 should work: %v", err)
	}

	// Test s2: custom method should fail.
	ct2, st2 := NewInMemoryTransports()
	ss2, err := s2.Connect(ctx, st2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss2.Close() })

	c2 := NewClient(testImpl, nil)
	cs2, err := c2.Connect(ctx, ct2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs2.Close() })

	err = callCustom(ctx, cs2.getConn(), "acme/custom", &emptyParams{}, &emptyResult{})
	if err == nil {
		t.Fatal("expected error calling custom method on s2")
	}
}

func TestCallCustomMethod(t *testing.T) {
	ctx := context.Background()
	s := NewServer(testImpl, nil)

	AddReceivingCustomMethod(s, "acme/search", func(ctx context.Context, ss *ServerSession, params *searchParams) (*searchResult, error) {
		return &searchResult{
			Hits:  []string{"result for " + params.Query},
			Total: 1,
		}, nil
	})

	ct, st := NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := NewClient(testImpl, nil)
	callSearch := AddSendingCustomMethod[*searchParams, *searchResult](c, "acme/search")

	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	result, err := callSearch(ctx, cs, &searchParams{Query: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0] != "result for hello" {
		t.Errorf("unexpected hits: %v", result.Hits)
	}
	if result.Total != 1 {
		t.Errorf("unexpected total: %d", result.Total)
	}
}

func TestCustomMethodStreamableHTTP(t *testing.T) {
	s := NewServer(testImpl, nil)

	type echoParams struct {
		ParamsBase
		Msg string `json:"msg"`
	}
	type echoResult struct {
		ResultBase
		Reply string `json:"reply"`
	}
	AddReceivingCustomMethod(s, "acme/echo", func(ctx context.Context, ss *ServerSession, params *echoParams) (*echoResult, error) {
		return &echoResult{Reply: "echo: " + params.Msg}, nil
	})

	handler := NewStreamableHTTPHandler(func(r *http.Request) *Server { return s }, nil)
	_ = handler
}
