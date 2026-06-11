// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestBatchFraming(t *testing.T) {
	// This test checks that the ndjsonFramer can read and write JSON batches.
	//
	// The framer is configured to write a batch size of 2, and we confirm that
	// nothing is sent over the wire until the second write, at which point both
	// messages become available.
	ctx := context.Background()

	r, w := io.Pipe()
	tport := newIOConn(rwc{r, w})
	tport.outgoingBatch = make([]jsonrpc.Message, 0, 2)
	t.Cleanup(func() { tport.Close() })

	// Read the two messages into a channel, for easy testing later.
	read := make(chan jsonrpc.Message)
	go func() {
		for range 2 {
			msg, _ := tport.Read(ctx)
			read <- msg
		}
	}()

	// The first write should not yet be observed by the reader.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(1), Method: "test"})
	select {
	case got := <-read:
		t.Fatalf("after one write, got message %v", got)
	default:
	}

	// ...but the second write causes both messages to be observed.
	tport.Write(ctx, &jsonrpc.Request{ID: jsonrpc2.Int64ID(2), Method: "test"})
	for _, want := range []int64{1, 2} {
		got := <-read
		if got := got.(*jsonrpc.Request).ID.Raw(); got != want {
			t.Errorf("got message #%d, want #%d", got, want)
		}
	}
}

func TestIOConnRead(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		want            string
		protocolVersion string
	}{
		{
			name:  "valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}}`,
			want:  "",
		},
		{
			name: "newline at the end of first valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}}
			`,
			want: "",
		},
		{
			name:  "bad data at the end of first valid json input",
			input: `{"jsonrpc":"2.0","id":1,"method":"test","params":{}},`,
			want:  "invalid trailing data at the end of stream",
		},
		{
			name:            "batching unknown protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "",
			protocolVersion: "",
		},
		{
			name:  "windows newline at the end of first valid json input",
			input: "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"test\",\"params\":{}}\r\n",
			want:  "",
		},
		{
			name:            "batching old protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "",
			protocolVersion: protocolVersion20241105,
		},
		{
			name:            "batching new protocol",
			input:           `[{"jsonrpc":"2.0","id":1,"method":"test1"},{"jsonrpc":"2.0","id":2,"method":"test2"}]`,
			want:            "JSON-RPC batching is not supported in 2025-06-18 and later (request version: 2025-06-18)",
			protocolVersion: protocolVersion20250618,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newIOConn(rwc{
				rc: io.NopCloser(strings.NewReader(tt.input)),
			})
			t.Cleanup(func() { tr.Close() })
			if tt.protocolVersion != "" {
				tr.sessionUpdated(ServerSessionState{
					InitializeParams: &InitializeParams{
						ProtocolVersion: tt.protocolVersion,
					},
				})
			}
			_, err := tr.Read(context.Background())
			if err == nil && tt.want != "" {
				t.Errorf("ioConn.Read() got nil error but wanted %v", tt.want)
			}
			if err != nil && err.Error() != tt.want {
				t.Errorf("ioConn.Read() = %v, want %v", err.Error(), tt.want)
			}
		})
	}
}

// TestSubscriptionsListen_InMemory verifies SEP-2575 subscriptions/listen
// over a single shared session (in-memory transport, semantically equivalent
// to STDIO). It exercises behavior that is harder to observe over streamable
// HTTP, where each listen lives in its own ephemeral session:
//
//   - two concurrent listens on the SAME session both deliver notifications;
//   - opt-in filtering: each listen receives only its opted-in notification
//     types, tagged with its own subscription ID;
//   - per-listen cancellation propagates over notifications/cancelled: when
//     the client cancels one listen's context, the server stops fanning out
//     notifications for that listen but keeps delivering to the other;
//   - the remaining listen continues to work after the first cancellation.
func TestSubscriptionsListen_InMemory(t *testing.T) {
	orig := supportedProtocolVersions
	supportedProtocolVersions = append([]string{protocolVersion20260630}, slices.Clone(orig)...)
	t.Cleanup(func() { supportedProtocolVersions = orig })

	ctx, topCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer topCancel()

	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "t1"}, sayHi)
	server.AddPrompt(&Prompt{Name: "p1"}, nil)

	ct, st := NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	type event struct {
		kind string
		id   string
	}
	events := make(chan event, 64)
	asEvent := func(kind string, raw any) event { return event{kind, fmt.Sprint(raw)} }

	client := NewClient(testImpl, &ClientOptions{
		ToolListChangedHandler: func(_ context.Context, req *ToolListChangedRequest) {
			events <- asEvent("tool", req.Params.Meta[MetaKeySubscriptionID])
		},
		PromptListChangedHandler: func(_ context.Context, req *PromptListChangedRequest) {
			events <- asEvent("prompt", req.Params.Meta[MetaKeySubscriptionID])
		},
	})
	client.AddReceivingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method == notificationSubscriptionsAck {
				if cr, ok := req.(*ClientRequest[*SubscriptionsAcknowledgedParams]); ok && cr.Params != nil {
					events <- asEvent("ack", cr.Params.Meta[MetaKeySubscriptionID])
				}
			}
			return next(ctx, method, req)
		}
	})

	cs, err := client.Connect(ctx, ct, &ClientSessionOptions{protocolVersion: protocolVersion20260630})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	startListen := func(notifs NotificationSubscriptions) context.CancelFunc {
		lctx, cancel := context.WithCancel(ctx)
		go func() {
			_ = cs.SubscriptionsListen(lctx, &SubscriptionsListenParams{Notifications: notifs})
		}()
		return cancel
	}
	waitFor := func(kind string) event {
		t.Helper()
		select {
		case e := <-events:
			if e.kind != kind {
				t.Fatalf("got event %q (id=%s), want kind %q", e.kind, e.id, kind)
			}
			return e
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %q event", kind)
			return event{}
		}
	}
	expectNoEvent := func(d time.Duration) {
		t.Helper()
		select {
		case e := <-events:
			t.Fatalf("unexpected event %q (id=%s)", e.kind, e.id)
		case <-time.After(d):
		}
	}

	cancelTools := startListen(NotificationSubscriptions{ToolsListChanged: true})
	cancelPrompts := startListen(NotificationSubscriptions{PromptsListChanged: true})

	ack1 := waitFor("ack")
	ack2 := waitFor("ack")
	if ack1.id == ack2.id {
		t.Fatalf("both acks share subscription ID %s; expected distinct per-listen IDs", ack1.id)
	}

	// Identify which ack belongs to which listen by triggering a tool change
	// and observing the tagged subscription ID on the notification.
	server.AddTool(&Tool{Name: "t2", InputSchema: &jsonschema.Schema{Type: "object"}}, nil)
	toolEv := waitFor("tool")
	if toolEv.id != ack1.id && toolEv.id != ack2.id {
		t.Fatalf("tool notif id %s matches neither ack (%s, %s)", toolEv.id, ack1.id, ack2.id)
	}
	toolSubID := toolEv.id
	promptSubID := ack2.id
	if ack1.id != toolSubID {
		promptSubID = ack1.id
	}
	expectNoEvent(notificationDelay * 5)

	server.AddPrompt(&Prompt{Name: "p2"}, nil)
	if e := waitFor("prompt"); e.id != promptSubID {
		t.Errorf("prompt notif id = %s, want %s", e.id, promptSubID)
	}
	expectNoEvent(notificationDelay * 5)

	// Cancel the tools listen. The SDK sends a notifications/cancelled to the
	// server, which flips the listen handler's ctx, which unblocks the
	// goroutine that removes the subscription from the server's index.
	cancelTools()

	// Give the cancellation a moment to propagate (notifications/cancelled
	// → server-side cancel → cleanup goroutine).
	time.Sleep(50 * time.Millisecond)

	// A new tool change must NOT reach the (cancelled) tools listen, while
	// the prompts listen continues to receive its notifications.
	server.AddTool(&Tool{Name: "t3", InputSchema: &jsonschema.Schema{Type: "object"}}, nil)
	expectNoEvent(notificationDelay * 20)

	server.AddPrompt(&Prompt{Name: "p3"}, nil)
	if e := waitFor("prompt"); e.id != promptSubID {
		t.Errorf("prompt notif after tools-cancel id = %s, want %s", e.id, promptSubID)
	}

	cancelPrompts()
	time.Sleep(50 * time.Millisecond)

	server.AddPrompt(&Prompt{Name: "p4"}, nil)
	expectNoEvent(notificationDelay * 20)
}
