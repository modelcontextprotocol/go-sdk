// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build go1.25

package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/jsonschema-go/jsonschema"
)

// TODO(maciej-kisiel): split this test into multiple test cases that target specific functionality.
func TestEndToEnd_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		var ct, st Transport = NewInMemoryTransports()

		// Channels to check if notification callbacks happened.
		// These test asynchronous sending of notifications after a small delay (see
		// Server.sendNotification).
		notificationChans := map[string]chan int{}
		for _, name := range []string{"initialized", "roots", "tools", "prompts", "resources", "progress_server", "progress_client", "resource_updated", "subscribe", "unsubscribe", "elicitation_complete"} {
			notificationChans[name] = make(chan int, 1)
		}

		waitForNotification := func(t *testing.T, name string) {
			t.Helper()
			time.Sleep(notificationDelay * 2)
			<-notificationChans[name]
		}

		sopts := &ServerOptions{
			InitializedHandler: func(context.Context, *InitializedRequest) {
				notificationChans["initialized"] <- 0
			},
			RootsListChangedHandler: func(context.Context, *RootsListChangedRequest) {
				notificationChans["roots"] <- 0
			},
			ProgressNotificationHandler: func(context.Context, *ProgressNotificationServerRequest) {
				notificationChans["progress_server"] <- 0
			},
			SubscribeHandler: func(context.Context, *SubscribeRequest) error {
				notificationChans["subscribe"] <- 0
				return nil
			},
			UnsubscribeHandler: func(context.Context, *UnsubscribeRequest) error {
				notificationChans["unsubscribe"] <- 0
				return nil
			},
		}
		s := NewServer(testImpl, sopts)
		AddTool(s, &Tool{
			Name:        "greet",
			Description: "say hi",
		}, sayHi)
		AddTool(s, &Tool{Name: "fail", InputSchema: &jsonschema.Schema{Type: "object"}},
			func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, any, error) {
				return nil, nil, errTestFailure
			})
		s.AddPrompt(codeReviewPrompt, codReviewPromptHandler)
		s.AddPrompt(&Prompt{Name: "fail"}, func(_ context.Context, _ *GetPromptRequest) (*GetPromptResult, error) {
			return nil, errTestFailure
		})
		s.AddResource(resource1, readHandler)
		s.AddResource(resource2, readHandler)

		// Connect the server.
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Collect(s.Sessions()); len(got) != 1 {
			t.Errorf("after connection, Clients() has length %d, want 1", len(got))
		}

		loggingMessages := make(chan *LoggingMessageParams, 100) // big enough for all logging
		opts := &ClientOptions{
			CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
				return &CreateMessageResult{Model: "aModel", Content: &TextContent{}}, nil
			},
			ElicitationHandler: func(ctx context.Context, req *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{
					Action: "accept",
					Content: map[string]any{
						"name":  "Test User",
						"email": "test@example.com",
					},
				}, nil
			},
			ToolListChangedHandler: func(context.Context, *ToolListChangedRequest) {
				notificationChans["tools"] <- 0
			},
			PromptListChangedHandler: func(context.Context, *PromptListChangedRequest) {
				notificationChans["prompts"] <- 0
			},
			ResourceListChangedHandler: func(context.Context, *ResourceListChangedRequest) {
				notificationChans["resources"] <- 0
			},
			LoggingMessageHandler: func(_ context.Context, req *LoggingMessageRequest) {
				loggingMessages <- req.Params
			},
			ProgressNotificationHandler: func(context.Context, *ProgressNotificationClientRequest) {
				notificationChans["progress_client"] <- 0
			},
			ResourceUpdatedHandler: func(context.Context, *ResourceUpdatedNotificationRequest) {
				notificationChans["resource_updated"] <- 0
			},
			ElicitationCompleteHandler: func(_ context.Context, req *ElicitationCompleteNotificationRequest) {
				notificationChans["elicitation_complete"] <- 0
			},
		}
		c := NewClient(testImpl, opts)
		rootAbs, err := filepath.Abs(filepath.FromSlash("testdata/files"))
		if err != nil {
			t.Fatal(err)
		}
		c.AddRoots(&Root{URI: "file://" + rootAbs})

		// Connect the client.
		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}

		waitForNotification(t, "initialized")
		if err := cs.Ping(ctx, nil); err != nil {
			t.Fatalf("ping failed: %v", err)
		}

		// ===== prompts =====
		t.Log("Testing prompts")
		{
			res, err := cs.ListPrompts(ctx, nil)
			if err != nil {
				t.Fatalf("prompts/list failed: %v", err)
			}
			wantPrompts := []*Prompt{
				{
					Name:        "code_review",
					Description: "do a code review",
					Arguments:   []*PromptArgument{{Name: "Code", Required: true}},
				},
				{Name: "fail"},
			}
			if diff := cmp.Diff(wantPrompts, res.Prompts); diff != "" {
				t.Fatalf("prompts/list mismatch (-want +got):\n%s", diff)
			}

			gotReview, err := cs.GetPrompt(ctx, &GetPromptParams{Name: "code_review", Arguments: map[string]string{"Code": "1+1"}})
			if err != nil {
				t.Fatal(err)
			}
			wantReview := &GetPromptResult{
				Description: "Code review prompt",
				Messages: []*PromptMessage{{
					Content: &TextContent{Text: "Please review the following code: 1+1"},
					Role:    "user",
				}},
			}
			if diff := cmp.Diff(wantReview, gotReview); diff != "" {
				t.Errorf("prompts/get 'code_review' mismatch (-want +got):\n%s", diff)
			}

			if _, err := cs.GetPrompt(ctx, &GetPromptParams{Name: "fail"}); err == nil || !strings.Contains(err.Error(), errTestFailure.Error()) {
				t.Errorf("fail returned unexpected error: got %v, want containing %v", err, errTestFailure)
			}

			s.AddPrompt(&Prompt{Name: "T"}, nil)
			waitForNotification(t, "prompts")
			s.RemovePrompts("T")
			waitForNotification(t, "prompts")
		}

		// ===== tools =====
		t.Log("Testing tools")
		{
			// ListTools is tested in client_list_test.go.
			gotHi, err := cs.CallTool(ctx, &CallToolParams{
				Name:      "greet",
				Arguments: map[string]any{"Name": "user"},
			})
			if err != nil {
				t.Fatal(err)
			}
			wantHi := &CallToolResult{
				Content: []Content{
					&TextContent{Text: "hi user"},
				},
			}
			if diff := cmp.Diff(wantHi, gotHi, ctrCmpOpts...); diff != "" {
				t.Errorf("tools/call 'greet' mismatch (-want +got):\n%s", diff)
			}

			gotFail, err := cs.CallTool(ctx, &CallToolParams{
				Name:      "fail",
				Arguments: map[string]any{},
			})
			// Counter-intuitively, when a tool fails, we don't expect an RPC error for
			// call tool: instead, the failure is embedded in the result.
			if err != nil {
				t.Fatal(err)
			}
			wantFail := &CallToolResult{
				IsError: true,
				Content: []Content{
					&TextContent{Text: errTestFailure.Error()},
				},
			}
			if diff := cmp.Diff(wantFail, gotFail, ctrCmpOpts...); diff != "" {
				t.Errorf("tools/call 'fail' mismatch (-want +got):\n%s", diff)
			}

			// Check output schema validation.
			badout := &Tool{
				Name: "badout",
				OutputSchema: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"x": {Type: "string"},
					},
				},
			}
			AddTool(s, badout, func(_ context.Context, _ *CallToolRequest, arg map[string]any) (*CallToolResult, map[string]any, error) {
				return nil, map[string]any{"x": 1}, nil
			})
			_, err = cs.CallTool(ctx, &CallToolParams{Name: "badout"})
			wantMsg := `has type "integer", want "string"`
			if err == nil || !strings.Contains(err.Error(), wantMsg) {
				t.Errorf("\ngot  %q\nwant error message containing %q", err, wantMsg)
			}

			// Check tools-changed notifications.
			s.AddTool(&Tool{Name: "T", InputSchema: &jsonschema.Schema{Type: "object"}}, nopHandler)
			waitForNotification(t, "tools")
			s.RemoveTools("T")
			waitForNotification(t, "tools")
		}

		// ===== resources =====
		t.Log("Testing resources")
		//TODO: fix for Windows
		if runtime.GOOS != "windows" {
			wantResources := []*Resource{resource2, resource1}
			lrres, err := cs.ListResources(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(wantResources, lrres.Resources); diff != "" {
				t.Errorf("resources/list mismatch (-want, +got):\n%s", diff)
			}

			template := &ResourceTemplate{
				Name:        "rt",
				MIMEType:    "text/template",
				URITemplate: "file:///{+filename}", // the '+' means that filename can contain '/'
			}
			s.AddResourceTemplate(template, readHandler)
			tres, err := cs.ListResourceTemplates(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff([]*ResourceTemplate{template}, tres.ResourceTemplates); diff != "" {
				t.Errorf("resources/list mismatch (-want, +got):\n%s", diff)
			}

			for _, tt := range []struct {
				uri      string
				mimeType string // "": not found; "text/plain": resource; "text/template": template
				fail     bool   // non-nil error returned
			}{
				{"file:///info.txt", "text/plain", false},
				{"file:///fail.txt", "", false},
				{"file:///template.txt", "text/template", false},
				{"file:///../private.txt", "", true}, // not found: escaping disallowed
			} {
				rres, err := cs.ReadResource(ctx, &ReadResourceParams{URI: tt.uri})
				if err != nil {
					if code := errorCode(err); code == CodeResourceNotFound {
						if tt.mimeType != "" {
							t.Errorf("%s: not found but expected it to be", tt.uri)
						}
					} else if !tt.fail {
						t.Errorf("%s: unexpected error %v", tt.uri, err)
					}
				} else {
					if tt.fail {
						t.Errorf("%s: unexpected success", tt.uri)
					} else if g, w := len(rres.Contents), 1; g != w {
						t.Errorf("got %d contents, wanted %d", g, w)
					} else {
						c := rres.Contents[0]
						if got := c.URI; got != tt.uri {
							t.Errorf("got uri %q, want %q", got, tt.uri)
						}
						if got := c.MIMEType; got != tt.mimeType {
							t.Errorf("%s: got MIME type %q, want %q", tt.uri, got, tt.mimeType)
						}
					}
				}
			}

			s.AddResource(&Resource{URI: "http://U"}, nil)
			waitForNotification(t, "resources")
			s.RemoveResources("http://U")
			waitForNotification(t, "resources")
		}

		// ===== roots =====
		t.Log("Testing roots")
		{
			rootRes, err := ss.ListRoots(ctx, &ListRootsParams{})
			if err != nil {
				t.Fatal(err)
			}
			gotRoots := rootRes.Roots
			wantRoots := slices.Collect(c.roots.all())
			if diff := cmp.Diff(wantRoots, gotRoots); diff != "" {
				t.Errorf("roots/list mismatch (-want +got):\n%s", diff)
			}

			c.AddRoots(&Root{URI: "U"})
			waitForNotification(t, "roots")
			c.RemoveRoots("U")
			waitForNotification(t, "roots")
		}

		// ===== sampling =====
		t.Log("Testing sampling")
		{
			// TODO: test that a client that doesn't have the handler returns CodeUnsupportedMethod.
			res, err := ss.CreateMessage(ctx, &CreateMessageParams{})
			if err != nil {
				t.Fatal(err)
			}
			if g, w := res.Model, "aModel"; g != w {
				t.Errorf("got %q, want %q", g, w)
			}
		}

		// ===== logging =====
		t.Log("Testing logging")
		{
			want := []*LoggingMessageParams{
				{
					Logger: "test",
					Level:  "warning",
					Data: map[string]any{
						"msg":     "first",
						"name":    "Pat",
						"logtest": true,
					},
				},
				{
					Logger: "test",
					Level:  "alert",
					Data: map[string]any{
						"msg":     "second",
						"count":   2.0,
						"logtest": true,
					},
				},
			}

			check := func(t *testing.T) {
				t.Helper()
				var got []*LoggingMessageParams
				// Read messages from this test until we've seen all we expect.
				for len(got) < len(want) {
					p := <-loggingMessages
					// Ignore logging from other tests.
					if m, ok := p.Data.(map[string]any); ok && m["logtest"] != nil {
						delete(m, "time")
						got = append(got, p)
					}
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("mismatch (-want, +got):\n%s", diff)
				}
			}

			// Use the LoggingMessage method directly.
			t.Log("Testing logging (direct)")
			{
				mustLog := func(level LoggingLevel, data any) {
					t.Helper()
					if err := ss.Log(ctx, &LoggingMessageParams{
						Logger: "test",
						Level:  level,
						Data:   data,
					}); err != nil {
						t.Fatal(err)
					}
				}

				// Nothing should be logged until the client sets a level.
				mustLog("info", "before")
				if err := cs.SetLoggingLevel(ctx, &SetLoggingLevelParams{Level: "warning"}); err != nil {
					t.Fatal(err)
				}
				mustLog("warning", want[0].Data)
				mustLog("debug", "nope")    // below the level
				mustLog("info", "negative") // below the level
				mustLog("alert", want[1].Data)
				check(t)
			}

			// Use the slog handler.
			t.Log("Testing logging (handler)")
			{
				// We can't check the "before SetLevel" behavior because it's already been set.
				// Not a big deal: that check is in LoggingMessage anyway.
				logger := slog.New(NewLoggingHandler(ss, &LoggingHandlerOptions{LoggerName: "test"}))
				logger.Warn("first", "name", "Pat", "logtest", true)
				logger.Debug("nope")    // below the level
				logger.Info("negative") // below the level
				logger.Log(ctx, LevelAlert, "second", "count", 2, "logtest", true)
				check(t)
			}
		}

		// ===== progress =====
		t.Log("Testing progress")
		{
			ss.NotifyProgress(ctx, &ProgressNotificationParams{
				ProgressToken: "token-xyz",
				Message:       "progress update",
				Progress:      0.5,
				Total:         2,
			})
			waitForNotification(t, "progress_client")

			cs.NotifyProgress(ctx, &ProgressNotificationParams{
				ProgressToken: "token-abc",
				Message:       "progress update",
				Progress:      1,
				Total:         2,
			})
			waitForNotification(t, "progress_server")
		}

		// ===== resource_subscriptions =====
		t.Log("Testing resource_subscriptions")
		{
			err := cs.Subscribe(ctx, &SubscribeParams{
				URI: "test",
			})
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			<-notificationChans["subscribe"]

			s.ResourceUpdated(ctx, &ResourceUpdatedNotificationParams{
				URI: "test",
			})
			waitForNotification(t, "resource_updated")

			err = cs.Unsubscribe(ctx, &UnsubscribeParams{
				URI: "test",
			})
			if err != nil {
				t.Fatal(err)
			}
			waitForNotification(t, "unsubscribe")

			// Verify the client does not receive the update after unsubscribing.
			s.ResourceUpdated(ctx, &ResourceUpdatedNotificationParams{
				URI: "test",
			})
			synctest.Wait()
			select {
			case <-notificationChans["resource_updated"]:
				t.Fatalf("resource updated after unsubscription")
			default:
				// Expected: no notification received
			}
		}

		// ===== elicitation =====
		t.Log("Testing elicitation")
		{
			result, err := ss.Elicit(ctx, &ElicitParams{
				Message: "Please provide information",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Action != "accept" {
				t.Errorf("got action %q, want %q", result.Action, "accept")
			}
		}

		// Disconnect.
		cs.Close()
		if err := ss.Wait(); err != nil {
			t.Errorf("server failed: %v", err)
		}

		// After disconnecting, neither client nor server should have any
		// connections.
		for range s.Sessions() {
			t.Errorf("unexpected client after disconnection")
		}
	})
}

func TestKeepAlive_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()

		ct, st := NewInMemoryTransports()

		serverOpts := &ServerOptions{
			KeepAlive: 100 * time.Millisecond,
		}
		s := NewServer(testImpl, serverOpts)
		AddTool(s, greetTool(), sayHi)

		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		clientOpts := &ClientOptions{
			KeepAlive: 100 * time.Millisecond,
		}
		c := NewClient(testImpl, clientOpts)
		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// Wait for a few keepalive cycles to ensure pings are working.
		// With synctest, this advances simulated time instantly.
		time.Sleep(300 * time.Millisecond)

		// Test that the connection is still alive by making a call
		result, err := cs.CallTool(ctx, &CallToolParams{
			Name:      "greet",
			Arguments: map[string]any{"Name": "user"},
		})
		if err != nil {
			t.Fatalf("call failed after keepalive: %v", err)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content in result")
		}
		if textContent, ok := result.Content[0].(*TextContent); !ok || textContent.Text != "hi user" {
			t.Fatalf("unexpected result: %v", result.Content[0])
		}
	})
}

func TestCancellation_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			start     = make(chan struct{})
			cancelled = make(chan struct{}, 1) // don't block the request
		)
		slowTool := func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
			start <- struct{}{}
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
			case <-time.After(5 * time.Second):
				return nil, nil, nil
			}
			return nil, nil, nil
		}
		cs, _, cleanup := basicConnection(t, func(s *Server) {
			AddTool(s, &Tool{Name: "slow", InputSchema: &jsonschema.Schema{Type: "object"}}, slowTool)
		})
		defer cleanup()

		ctx, cancel := context.WithCancel(context.Background())
		go cs.CallTool(ctx, &CallToolParams{Name: "slow"})
		<-start
		cancel()

		<-cancelled
	})
}

func TestKeepAliveFailure_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()

		// This test doesn't pinpoint keepalive detected failures well due to the fact that in-memory transports
		// propagate `io.EOF` synchronously, causing the transport level connection to be closed immediately.
		// TODO: propose better transports that would allow testing this scenario precisely.
		ct, st := NewInMemoryTransports()

		// Server without keepalive (to test one-sided keepalive)
		s := NewServer(testImpl, nil)
		AddTool(s, greetTool(), sayHi)
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}

		// Client with short keepalive
		clientOpts := &ClientOptions{
			KeepAlive: 50 * time.Millisecond,
		}
		c := NewClient(testImpl, clientOpts)
		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// Let the connection establish properly first
		synctest.Wait()

		// simulate ping failure
		ss.Close()

		time.Sleep(100 * time.Millisecond)
		synctest.Wait()

		_, err = cs.CallTool(ctx, &CallToolParams{
			Name:      "greet",
			Arguments: map[string]any{"Name": "user"},
		})
		if err != nil && (errors.Is(err, ErrConnectionClosed) || strings.Contains(err.Error(), "connection closed")) {
			return // Test passed
		}

		t.Errorf("expected connection to be closed by keepalive, but it wasn't. Last error: %v", err)
	})
}

func TestSynchronousNotifications_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var toolsChanged atomic.Int32
		clientOpts := &ClientOptions{
			ToolListChangedHandler: func(ctx context.Context, req *ToolListChangedRequest) {
				toolsChanged.Add(1)
			},
			CreateMessageHandler: func(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResult, error) {
				// See the comment after "from server" below.
				if n := toolsChanged.Load(); n != 1 {
					return nil, fmt.Errorf("got %d tools-changed notification, wanted 1", n)
				}
				// TODO(rfindley): investigate the error returned from this test if
				// CreateMessageResult is new(CreateMessageResult): it's a mysterious
				// unmarshalling error that we should improve.
				return &CreateMessageResult{Content: &TextContent{}}, nil
			},
		}
		client := NewClient(testImpl, clientOpts)

		var rootsChanged atomic.Bool
		serverOpts := &ServerOptions{
			RootsListChangedHandler: func(_ context.Context, req *RootsListChangedRequest) {
				rootsChanged.Store(true)
			},
		}
		server := NewServer(testImpl, serverOpts)
		addTool := func(s *Server) {
			AddTool(s, &Tool{Name: "tool"}, func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
				if !rootsChanged.Load() {
					return nil, nil, fmt.Errorf("didn't get root change notification")
				}
				return new(CallToolResult), nil, nil
			})
		}
		cs, ss, cleanup := basicClientServerConnection(t, client, server, addTool)
		defer cleanup()

		t.Log("from client")
		{
			client.AddRoots(&Root{Name: "myroot", URI: "file://foo"})
			res, err := cs.CallTool(context.Background(), &CallToolParams{Name: "tool"})
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if res.IsError {
				t.Errorf("tool error: %v", res.Content[0].(*TextContent).Text)
			}
		}

		t.Log("from server")
		{
			// Despite all this tool-changed activity, we expect only one notification.
			for range 10 {
				server.RemoveTools("tool")
				addTool(server)
			}

			time.Sleep(notificationDelay * 2) // Wait for delayed notification.
			if _, err := ss.CreateMessage(context.Background(), new(CreateMessageParams)); err != nil {
				t.Errorf("CreateMessage failed: %v", err)
			}
		}

	})
}

func TestNoDistributedDeadlock_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// This test verifies that calls are asynchronous, and so it's not possible
		// to have a distributed deadlock.
		//
		// The setup creates potential deadlock for both the client and server: the
		// client sends a call to tool1, which itself calls createMessage, which in
		// turn calls tool2, which calls ping.
		//
		// If the server were not asynchronous, the call to tool2 would hang. If the
		// client were not asynchronous, the call to ping would hang.
		//
		// Such a scenario is unlikely in practice, but is still theoretically
		// possible, and in any case making tool calls asynchronous by default
		// delegates synchronization to the user.
		clientOpts := &ClientOptions{
			CreateMessageHandler: func(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResult, error) {
				req.Session.CallTool(ctx, &CallToolParams{Name: "tool2"})
				return &CreateMessageResult{Content: &TextContent{}}, nil
			},
		}
		client := NewClient(testImpl, clientOpts)
		cs, _, cleanup := basicClientServerConnection(t, client, nil, func(s *Server) {
			AddTool(s, &Tool{Name: "tool1"}, func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
				req.Session.CreateMessage(ctx, new(CreateMessageParams))
				return new(CallToolResult), nil, nil
			})
			AddTool(s, &Tool{Name: "tool2"}, func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
				req.Session.Ping(ctx, nil)
				return new(CallToolResult), nil, nil
			})
		})
		defer cleanup()

		if _, err := cs.CallTool(context.Background(), &CallToolParams{Name: "tool1"}); err != nil {
			// should not deadlock
			t.Fatalf("CallTool failed: %v", err)
		}
	})
}
