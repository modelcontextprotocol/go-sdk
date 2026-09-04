// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestResourceSubscriptions_SubscribeClosedSession verifies that Subscribe on a
// closed session reports the failure instead of silently succeeding, and does
// not leave a local resourceSubs entry behind. See
// https://github.com/modelcontextprotocol/go-sdk/issues/1171.
func TestResourceSubscriptions_SubscribeClosedSession(t *testing.T) {
	server := resourceSubServer(t, make(chan string, 8), make(chan string, 8))
	ct, st := NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := NewClient(testImpl, &ClientOptions{})
	cs, err := c.Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}

	err = cs.Subscribe(ctx, &SubscribeParams{URI: "file:///r1"})
	if err == nil {
		t.Fatalf("Subscribe on a closed session returned nil")
	}
	if !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("Subscribe on a closed session returned %v, want ErrConnectionClosed", err)
	}

	cs.resourceSubsMu.Lock()
	_, exists := cs.resourceSubs["file:///r1"]
	cs.resourceSubsMu.Unlock()
	if exists {
		t.Fatalf("resourceSubs keeps an entry after a failed Subscribe")
	}
}

// TestResourceSubscriptions_SubscribeListenFailureRollsBack verifies that a
// failed subscriptions/listen send rolls back the local registration, so the
// URI is not misreported as subscribed and a later Subscribe retries instead
// of treating it as an idempotent no-op.
func TestResourceSubscriptions_SubscribeListenFailureRollsBack(t *testing.T) {
	server := resourceSubServer(t, make(chan string, 8), make(chan string, 8))
	ct, st := NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listenErr := errors.New("subscriptions/listen refused")
	c := NewClient(testImpl, &ClientOptions{})
	c.AddSendingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method == methodSubscriptionsListen {
				return nil, listenErr
			}
			return next(ctx, method, req)
		}
	})
	cs, err := c.Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	if err := cs.Subscribe(ctx, &SubscribeParams{URI: "file:///r1"}); !errors.Is(err, listenErr) {
		t.Fatalf("Subscribe returned %v, want %v", err, listenErr)
	}

	cs.resourceSubsMu.Lock()
	_, exists := cs.resourceSubs["file:///r1"]
	cs.resourceSubsMu.Unlock()
	if exists {
		t.Fatalf("resourceSubs keeps an entry after the listen stream failed to start")
	}

	// The failed registration was rolled back, so a retry must attempt the
	// listen again instead of returning nil as an idempotent no-op.
	if err := cs.Subscribe(ctx, &SubscribeParams{URI: "file:///r1"}); !errors.Is(err, listenErr) {
		t.Fatalf("second Subscribe returned %v, want %v (retry after rollback)", err, listenErr)
	}
}

// TestResourceSubscriptions_SubscribeCloseRace verifies the registration
// invariant when Subscribe races Close: a failed Subscribe must not leave a
// local resourceSubs entry behind. A successful Subscribe either owns the
// registration or the entry was cleaned up by the racing Close.
func TestResourceSubscriptions_SubscribeCloseRace(t *testing.T) {
	const rounds = 50

	for i := 0; i < rounds; i++ {
		server := resourceSubServer(t, make(chan string, 8), make(chan string, 8))
		ct, st := NewInMemoryTransports()
		ss, err := server.Connect(context.Background(), st, nil)
		if err != nil {
			t.Fatalf("server connect: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		c := NewClient(testImpl, &ClientOptions{})
		cs, err := c.Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
		if err != nil {
			t.Fatalf("client connect: %v", err)
		}

		var wg sync.WaitGroup
		var subErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			subErr = cs.Subscribe(ctx, &SubscribeParams{URI: "file:///r1"})
		}()
		go func() {
			defer wg.Done()
			_ = cs.Close()
		}()
		wg.Wait()

		if subErr != nil {
			cs.resourceSubsMu.Lock()
			_, exists := cs.resourceSubs["file:///r1"]
			cs.resourceSubsMu.Unlock()
			if exists {
				t.Fatalf("round %d: failed Subscribe (%v) left a resourceSubs entry", i, subErr)
			}
		}

		cancel()
		_ = ss.Close()
	}
}
