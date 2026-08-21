// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
package mcp

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func getServerSession(t *testing.T) (*ClientSession, *ServerSession) {
	t.Helper()
	ctx := context.Background()
	s := NewServer(&Implementation{Name: "s", Version: "0"}, nil)
	AddTool(s, &Tool{Name: "t"}, sayHi)

	ct, st := NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := NewClient(&Implementation{Name: "c", Version: "0"}, &ClientOptions{})
	cs, err := c.Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	var ss *ServerSession
	for x := range s.Sessions() {
		ss = x
		break
	}
	if ss == nil {
		t.Fatal("no server session found")
	}
	return cs, ss
}

func listenIDsCount(ss *ServerSession) int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.listenIDs)
}

// A completed listen stream must not leave a stale entry in the session.
func TestListenPrunedAfterSingleCompletion(t *testing.T) {
	cs, ss := getServerSession(t)
	ctx := context.Background()

	lctx, cancel := context.WithCancel(ctx)
	go cs.subscriptionsListen(lctx, &SubscriptionsListenParams{
		Notifications: &NotificationSubscriptions{ToolsListChanged: true},
	})
	time.Sleep(30 * time.Millisecond)
	cancel() // peer cancels: server handler returns
	time.Sleep(30 * time.Millisecond)

	if n := listenIDsCount(ss); n != 0 {
		t.Fatalf("completed listen left %d stale entry/ies", n)
	}
}

// Completed listens must not accumulate: the slice grows without bound today.
func TestListenIDsDoNotAccumulate(t *testing.T) {
	cs, ss := getServerSession(t)
	ctx := context.Background()

	const cycles = 15
	for i := 0; i < cycles; i++ {
		lctx, cancel := context.WithCancel(ctx)
		go cs.subscriptionsListen(lctx, &SubscriptionsListenParams{
			Notifications: &NotificationSubscriptions{ToolsListChanged: true},
		})
		time.Sleep(15 * time.Millisecond)
		cancel()
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)

	if n := listenIDsCount(ss); n != 0 {
		t.Fatalf("listenIDs grew unbounded: %d stale entries after %d completed listens", n, cycles)
	}
}

// The leak is reachable through the public Subscribe/Unsubscribe API: every
// subscription opens a listen stream and unsubscribing completes it, so a real
// client cycling subscriptions on a long-lived session leaks one entry per cycle.
func TestListenIDsLeakViaPublicSubscribeUnsubscribe(t *testing.T) {
	cs, ss := getServerSession(t)
	ctx := context.Background()

	const cycles = 3
	for i := 0; i < cycles; i++ {
		uri := fmt.Sprintf("resource://cycle-%d", i)
		if err := cs.Subscribe(ctx, &SubscribeParams{URI: uri}); err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
		time.Sleep(15 * time.Millisecond)
		if err := cs.Unsubscribe(ctx, &UnsubscribeParams{URI: uri}); err != nil {
			t.Fatalf("Unsubscribe %d: %v", i, err)
		}
	}
	time.Sleep(30 * time.Millisecond)

	if n := listenIDsCount(ss); n != 0 {
		t.Fatalf("public Subscribe/Unsubscribe leaked %d stale listenIDs after %d cycles", n, cycles)
	}
}
