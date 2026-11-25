// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"testing"
	"time"
)

// TestSubscriptionsListen_EndedListenRetiredWhenUnsubscribeHandlerErrors
// verifies that a subscriptions/listen that has ended is no longer registered
// even when the UnsubscribeHandler returns an error. The handler is called from
// the listen's teardown after its context is cancelled, so a handler that
// returns ctx.Err() is enough to hit this.
func TestSubscriptionsListen_EndedListenRetiredWhenUnsubscribeHandlerErrors(t *testing.T) {
	const uri = "file:///r1"
	unsubscribed := make(chan struct{}, 8)
	server := NewServer(testImpl, &ServerOptions{
		SubscribeHandler: func(context.Context, *SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(ctx context.Context, _ *UnsubscribeRequest) error {
			unsubscribed <- struct{}{}
			return ctx.Err()
		},
	})
	server.AddResource(&Resource{Name: "r1", URI: uri}, nil)

	ct, st := NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	events := make(chan subListenEvent, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := resourceSubClient(events).Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	listenCtx, cancelListen := context.WithCancel(ctx)
	defer cancelListen()
	go cs.subscriptionsListen(listenCtx, &SubscriptionsListenParams{
		Notifications: &NotificationSubscriptions{ResourceSubscriptions: []string{uri}},
	})
	waitSubListenEvent(t, events, "ack")

	cancelListen()
	select {
	case <-unsubscribed:
	case <-time.After(5 * time.Second):
		t.Fatal("UnsubscribeHandler was not called for the ended listen")
	}

	// The handler has returned; the ended listen must not stay registered.
	waitUntil(t, 5*time.Second, "the ended listen to be retired", func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.resourceSubscriptions[uri]) == 0
	})

	// And a resource update must not be delivered for the ended listen.
	if err := server.ResourceUpdated(ctx, &ResourceUpdatedNotificationParams{URI: uri}); err != nil {
		t.Fatalf("ResourceUpdated: %v", err)
	}
	select {
	case e := <-events:
		t.Fatalf("ended listen received %q notification (id=%s)", e.kind, e.id)
	case <-time.After(notificationDelay * 10):
	}
}
