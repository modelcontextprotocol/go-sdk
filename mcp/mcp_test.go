// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

type hiParams struct {
	Name string
}

// TODO(jba): after schemas are stateless (WIP), this can be a variable.
func greetTool() *Tool { return &Tool{Name: "greet", Description: "say hi"} }

func sayHi(ctx context.Context, req *CallToolRequest, args hiParams) (*CallToolResult, any, error) {
	if err := req.Session.Ping(ctx, nil); err != nil {
		return nil, nil, fmt.Errorf("ping failed: %v", err)
	}
	return &CallToolResult{Content: []Content{&TextContent{Text: "hi " + args.Name}}}, nil, nil
}

var codeReviewPrompt = &Prompt{
	Name:        "code_review",
	Description: "do a code review",
	Arguments:   []*PromptArgument{{Name: "Code", Required: true}},
}

func codReviewPromptHandler(_ context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
	return &GetPromptResult{
		Description: "Code review prompt",
		Messages: []*PromptMessage{
			{Role: "user", Content: &TextContent{Text: "Please review the following code: " + req.Params.Arguments["Code"]}},
		},
	}, nil
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	var ct, st Transport = NewInMemoryTransports()

	// Channels to check if notification callbacks happened.
	notificationChans := map[string]chan int{}
	for _, name := range []string{"initialized", "roots", "tools", "prompts", "resources", "progress_server", "progress_client", "resource_updated", "subscribe", "unsubscribe"} {
		notificationChans[name] = make(chan int, 1)
	}
	waitForNotification := func(t *testing.T, name string) {
		t.Helper()
		select {
		case <-notificationChans[name]:
		case <-time.After(time.Second):
			t.Fatalf("%s handler never called", name)
		}
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

	// Wait for the server to exit after the client closes its connection.
	var clientWG sync.WaitGroup
	clientWG.Add(1)
	go func() {
		if err := ss.Wait(); err != nil {
			t.Errorf("server failed: %v", err)
		}
		clientWG.Done()
	}()

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
	t.Run("prompts", func(t *testing.T) {
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
	})

	t.Run("tools", func(t *testing.T) {
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
	})

	t.Run("resources", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("TODO: fix for Windows")
		}
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
				if code := errorCode(err); code == codeResourceNotFound {
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
	})
	t.Run("roots", func(t *testing.T) {
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
	})
	t.Run("sampling", func(t *testing.T) {
		// TODO: test that a client that doesn't have the handler returns CodeUnsupportedMethod.
		res, err := ss.CreateMessage(ctx, &CreateMessageParams{})
		if err != nil {
			t.Fatal(err)
		}
		if g, w := res.Model, "aModel"; g != w {
			t.Errorf("got %q, want %q", g, w)
		}
	})
	t.Run("logging", func(t *testing.T) {
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
				select {
				case p := <-loggingMessages:
					// Ignore logging from other tests.
					if m, ok := p.Data.(map[string]any); ok && m["logtest"] != nil {
						delete(m, "time") // remove time because it changes
						got = append(got, p)
					}
				case <-time.After(time.Second):
					t.Fatal("timed out waiting for log messages")
				}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("mismatch (-want, +got):\n%s", diff)
			}
		}

		t.Run("direct", func(t *testing.T) { // Use the LoggingMessage method directly.

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
		})

		t.Run("handler", func(t *testing.T) { // Use the slog handler.
			// We can't check the "before SetLevel" behavior because it's already been set.
			// Not a big deal: that check is in LoggingMessage anyway.
			logger := slog.New(NewLoggingHandler(ss, &LoggingHandlerOptions{LoggerName: "test"}))
			logger.Warn("first", "name", "Pat", "logtest", true)
			logger.Debug("nope")    // below the level
			logger.Info("negative") // below the level
			logger.Log(ctx, LevelAlert, "second", "count", 2, "logtest", true)
			check(t)
		})
	})
	t.Run("progress", func(t *testing.T) {
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
	})

	t.Run("resource_subscriptions", func(t *testing.T) {
		err := cs.Subscribe(ctx, &SubscribeParams{
			URI: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
		waitForNotification(t, "subscribe")
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
		select {
		case <-notificationChans["resource_updated"]:
			t.Fatalf("resource updated after unsubscription")
		case <-time.After(time.Second):
		}
	})

	t.Run("elicitation", func(t *testing.T) {
		result, err := ss.Elicit(ctx, &ElicitParams{
			Message: "Please provide information",
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != "accept" {
			t.Errorf("got action %q, want %q", result.Action, "accept")
		}
	})

	// Disconnect.
	cs.Close()
	clientWG.Wait()

	// After disconnecting, neither client nor server should have any
	// connections.
	for range s.Sessions() {
		t.Errorf("unexpected client after disconnection")
	}
}

// Registry of values to be referenced in tests.
var (
	errTestFailure = errors.New("mcp failure")

	resource1 = &Resource{
		Name:     "public",
		MIMEType: "text/plain",
		URI:      "file:///info.txt",
	}
	resource2 = &Resource{
		Name:     "public", // names are not unique IDs
		MIMEType: "text/plain",
		URI:      "file:///fail.txt",
	}
	resource3 = &Resource{
		Name:     "info",
		MIMEType: "text/plain",
		URI:      "embedded:info",
	}
	readHandler = fileResourceHandler("testdata/files")
)

var embeddedResources = map[string]string{
	"info": "This is the MCP test server.",
}

func handleEmbeddedResource(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
	u, err := url.Parse(req.Params.URI)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "embedded" {
		return nil, fmt.Errorf("wrong scheme: %q", u.Scheme)
	}
	key := u.Opaque
	text, ok := embeddedResources[key]
	if !ok {
		return nil, fmt.Errorf("no embedded resource named %q", key)
	}
	return &ReadResourceResult{
		Contents: []*ResourceContents{
			{URI: req.Params.URI, MIMEType: "text/plain", Text: text},
		},
	}, nil
}

// errorCode returns the code associated with err.
// If err is nil, it returns 0.
// If there is no code, it returns -1.
func errorCode(err error) int64 {
	if err == nil {
		return 0
	}
	var werr *jsonrpc2.WireError
	if errors.As(err, &werr) {
		return werr.Code
	}
	return -1
}

// basicConnection returns a new basic client-server connection, with the server
// configured via the provided function.
//
// The caller should cancel either the client connection or server connection
// when the connections are no longer needed.
//
// The returned func cleans up by closing the client and waiting for the server
// to shut down.
func basicConnection(t *testing.T, config func(*Server)) (*ClientSession, *ServerSession, func()) {
	return basicClientServerConnection(t, nil, nil, config)
}

// basicClientServerConnection creates a basic connection between client and
// server. If either client or server is nil, empty implementations are used.
//
// The provided function may be used to configure features on the resulting
// server, prior to connection.
//
// The caller should cancel either the client connection or server connection
// when the connections are no longer needed.
//
// The returned func cleans up by closing the client and waiting for the server
// to shut down.
func basicClientServerConnection(t *testing.T, client *Client, server *Server, config func(*Server)) (*ClientSession, *ServerSession, func()) {
	t.Helper()

	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	if server == nil {
		server = NewServer(testImpl, nil)
	}
	if config != nil {
		config(server)
	}
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	if client == nil {
		client = NewClient(testImpl, nil)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, ss, func() {
		cs.Close()
		ss.Wait()
	}
}

func TestServerClosing(t *testing.T) {
	cs, ss, cleanup := basicConnection(t, func(s *Server) {
		AddTool(s, greetTool(), sayHi)
	})
	defer cleanup()

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := cs.Wait(); err != nil {
			t.Errorf("server connection failed: %v", err)
		}
		wg.Done()
	}()
	if _, err := cs.CallTool(ctx, &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"Name": "user"},
	}); err != nil {
		t.Fatalf("after connecting: %v", err)
	}
	ss.Close()
	wg.Wait()
	if _, err := cs.CallTool(ctx, &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "user"},
	}); !errors.Is(err, ErrConnectionClosed) {
		t.Errorf("after disconnection, got error %v, want EOF", err)
	}
}

func TestBatching(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	_, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient(testImpl, nil)
	// TODO: this test is broken, because increasing the batch size here causes
	// 'initialize' to block. Therefore, we can only test with a size of 1.
	// Since batching is being removed, we can probably just delete this.
	const batchSize = 1
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	errs := make(chan error, batchSize)
	for i := range batchSize {
		go func() {
			_, err := cs.ListTools(ctx, nil)
			errs <- err
		}()
		time.Sleep(2 * time.Millisecond)
		if i < batchSize-1 {
			select {
			case <-errs:
				t.Errorf("ListTools: unexpected result for incomplete batch: %v", err)
			default:
			}
		}
	}
}

func TestCancellation(t *testing.T) {
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
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for cancellation")
	}
}

func TestMiddleware(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the server to exit after the client closes its connection.
	defer func() {
		if err := ss.Wait(); err != nil {
			t.Errorf("server failed: %v", err)
		}
	}()

	var sbuf, cbuf bytes.Buffer
	sbuf.WriteByte('\n')
	cbuf.WriteByte('\n')

	// "1" is the outer middleware layer, called first; then "2" is called, and finally
	// the default dispatcher.
	s.AddSendingMiddleware(traceCalls[*ServerSession](&sbuf, "S1"), traceCalls[*ServerSession](&sbuf, "S2"))
	s.AddReceivingMiddleware(traceCalls[*ServerSession](&sbuf, "R1"), traceCalls[*ServerSession](&sbuf, "R2"))

	c := NewClient(testImpl, nil)
	c.AddSendingMiddleware(traceCalls[*ClientSession](&cbuf, "S1"), traceCalls[*ClientSession](&cbuf, "S2"))
	c.AddReceivingMiddleware(traceCalls[*ClientSession](&cbuf, "R1"), traceCalls[*ClientSession](&cbuf, "R2"))

	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.ListRoots(ctx, nil); err != nil {
		t.Fatal(err)
	}

	wantServer := `
R1 >initialize
R2 >initialize
R2 <initialize
R1 <initialize
R1 >notifications/initialized
R2 >notifications/initialized
R2 <notifications/initialized
R1 <notifications/initialized
R1 >tools/list
R2 >tools/list
R2 <tools/list
R1 <tools/list
S1 >roots/list
S2 >roots/list
S2 <roots/list
S1 <roots/list
`
	if diff := cmp.Diff(wantServer, sbuf.String()); diff != "" {
		t.Errorf("server mismatch (-want, +got):\n%s", diff)
	}

	wantClient := `
S1 >initialize
S2 >initialize
S2 <initialize
S1 <initialize
S1 >notifications/initialized
S2 >notifications/initialized
S2 <notifications/initialized
S1 <notifications/initialized
S1 >tools/list
S2 >tools/list
S2 <tools/list
S1 <tools/list
R1 >roots/list
R2 >roots/list
R2 <roots/list
R1 <roots/list
`
	if diff := cmp.Diff(wantClient, cbuf.String()); diff != "" {
		t.Errorf("client mismatch (-want, +got):\n%s", diff)
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func TestNoJSONNull(t *testing.T) {
	ctx := context.Background()
	var ct, st Transport = NewInMemoryTransports()

	// Collect logs, to sanity check that we don't write JSON null anywhere.
	var logbuf safeBuffer
	ct = &LoggingTransport{Transport: ct, Writer: &logbuf}

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListPrompts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResources(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResourceTemplates(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.ListRoots(ctx, nil); err != nil {
		t.Fatal(err)
	}

	cs.Close()
	ss.Wait()

	logs := logbuf.Bytes()
	if i := bytes.Index(logs, []byte("null")); i >= 0 {
		start := max(i-20, 0)
		end := min(i+20, len(logs))
		t.Errorf("conformance violation: MCP logs contain JSON null: %s", "..."+string(logs[start:end])+"...")
	}
}

// traceCalls creates a middleware function that prints the method before and after each call
// with the given prefix.
func traceCalls[S Session](w io.Writer, prefix string) Middleware {
	return func(h MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			fmt.Fprintf(w, "%s >%s\n", prefix, method)
			defer fmt.Fprintf(w, "%s <%s\n", prefix, method)
			return h(ctx, method, req)
		}
	}
}

func nopHandler(context.Context, *CallToolRequest) (*CallToolResult, error) {
	return nil, nil
}

func TestKeepAlive(t *testing.T) {
	// TODO: try to use the new synctest package for this test once we upgrade to Go 1.24+.
	// synctest would allow us to control time and avoid the time.Sleep calls, making the test
	// faster and more deterministic.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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

	// Wait for a few keepalive cycles to ensure pings are working
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
}

func TestElicitationUnsupportedMethod(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Server
	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	// Client without ElicitationHandler
	c := NewClient(testImpl, &ClientOptions{
		CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
			return &CreateMessageResult{Model: "aModel", Content: &TextContent{}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Test that elicitation fails when no handler is provided
	_, err = ss.Elicit(ctx, &ElicitParams{
		Message: "This should fail",
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"test": {Type: "string"},
			},
		},
	})

	if err == nil {
		t.Error("expected error when ElicitationHandler is not provided, got nil")
	}
	if code := errorCode(err); code != codeUnsupportedMethod {
		t.Errorf("got error code %d, want %d (CodeUnsupportedMethod)", code, codeUnsupportedMethod)
	}
	if !strings.Contains(err.Error(), "does not support elicitation") {
		t.Errorf("error should mention unsupported elicitation, got: %v", err)
	}
}

func TestElicitationSchemaValidation(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept", Content: map[string]any{"test": "value"}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Test valid schemas - these should not return errors
	validSchemas := []struct {
		name   string
		schema *jsonschema.Schema
	}{
		{
			name:   "nil schema",
			schema: nil,
		},
		{
			name: "empty object schema",
			schema: &jsonschema.Schema{
				Type: "object",
			},
		},
		{
			name: "simple string property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
			},
		},
		{
			name: "string with valid formats",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"email":    {Type: "string", Format: "email"},
					"website":  {Type: "string", Format: "uri"},
					"birthday": {Type: "string", Format: "date"},
					"created":  {Type: "string", Format: "date-time"},
				},
			},
		},
		{
			name: "string with constraints",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MinLength: ptr(1), MaxLength: ptr(100)},
				},
			},
		},
		{
			name: "number with constraints",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"age":   {Type: "integer", Minimum: ptr(0.0), Maximum: ptr(150.0)},
					"score": {Type: "number", Minimum: ptr(0.0), Maximum: ptr(100.0)},
				},
			},
		},
		{
			name: "boolean with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "boolean", Default: json.RawMessage("true")},
				},
			},
		},
		{
			name: "string enum",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"status": {
						Type: "string",
						Enum: []any{
							"active",
							"inactive",
							"pending",
						},
					},
				},
			},
		},
		{
			name: "enum with matching enumNames",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Type: "string",
						Enum: []any{
							"high",
							"medium",
							"low",
						},
						Extra: map[string]any{
							"enumNames": []any{"High Priority", "Medium Priority", "Low Priority"},
						},
					},
				},
			},
		},
	}

	for _, tc := range validSchemas {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			_, err := ss.Elicit(ctx, &ElicitParams{
				Message:         "Test valid schema: " + tc.name,
				RequestedSchema: tc.schema,
			})
			if err != nil {
				t.Errorf("expected no error for valid schema %q, got: %v", tc.name, err)
			}
		})
	}

	// Test invalid schemas - these should return errors
	invalidSchemas := []struct {
		name          string
		schema        *jsonschema.Schema
		expectedError string
	}{
		{
			name: "root schema non-object type",
			schema: &jsonschema.Schema{
				Type: "string",
			},
			expectedError: "elicit schema must be of type 'object', got \"string\"",
		},
		{
			name: "nested object property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"user": {
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"name": {Type: "string"},
						},
					},
				},
			},
			expectedError: "elicit schema property \"user\" contains nested properties, only primitive properties are allowed",
		},
		{
			name: "property with explicit object type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"config": {Type: "object"},
				},
			},
			expectedError: "elicit schema property \"config\" has unsupported type \"object\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "array property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"tags": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
				},
			},
			expectedError: "elicit schema property \"tags\" has unsupported type \"array\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "array without items",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"items": {Type: "array"},
				},
			},
			expectedError: "elicit schema property \"items\" has unsupported type \"array\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "unsupported string format",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"phone": {Type: "string", Format: "phone"},
				},
			},
			expectedError: "elicit schema property \"phone\" has unsupported format \"phone\", only email, uri, date, and date-time are allowed",
		},
		{
			name: "unsupported type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"data": {Type: "null"},
				},
			},
			expectedError: "elicit schema property \"data\" has unsupported type \"null\"",
		},
		{
			name: "string with invalid minLength",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MinLength: ptr(-1)},
				},
			},
			expectedError: "elicit schema property \"name\" has invalid minLength -1, must be non-negative",
		},
		{
			name: "string with invalid maxLength",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MaxLength: ptr(-5)},
				},
			},
			expectedError: "elicit schema property \"name\" has invalid maxLength -5, must be non-negative",
		},
		{
			name: "string with maxLength less than minLength",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MinLength: ptr(10), MaxLength: ptr(5)},
				},
			},
			expectedError: "elicit schema property \"name\" has maxLength 5 less than minLength 10",
		},
		{
			name: "number with maximum less than minimum",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"score": {Type: "number", Minimum: ptr(100.0), Maximum: ptr(50.0)},
				},
			},
			expectedError: "elicit schema property \"score\" has maximum 50 less than minimum 100",
		},
		{
			name: "boolean with invalid default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "boolean", Default: json.RawMessage(`"not-a-boolean"`)},
				},
			},
			expectedError: "elicit schema property \"enabled\" has invalid default value, must be a boolean",
		},
		{
			name: "enum with mismatched enumNames length",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Type: "string",
						Enum: []any{
							"high",
							"medium",
							"low",
						},
						Extra: map[string]any{
							"enumNames": []any{"High Priority", "Medium Priority"}, // Only 2 names for 3 values
						},
					},
				},
			},
			expectedError: "elicit schema property \"priority\" has 3 enum values but 2 enumNames, they must match",
		},
		{
			name: "enum with invalid enumNames type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"status": {
						Type: "string",
						Enum: []any{
							"active",
							"inactive",
						},
						Extra: map[string]any{
							"enumNames": "not an array", // Should be array
						},
					},
				},
			},
			expectedError: "elicit schema property \"status\" has invalid enumNames type, must be an array",
		},
		{
			name: "enum without explicit type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Enum: []any{
							"high",
							"medium",
							"low",
						},
					},
				},
			},
			expectedError: "elicit schema property \"priority\" has unsupported type \"\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "untyped property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"data": {},
				},
			},
			expectedError: "elicit schema property \"data\" has unsupported type \"\", only string, number, integer, and boolean are allowed",
		},
	}

	for _, tc := range invalidSchemas {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			_, err := ss.Elicit(ctx, &ElicitParams{
				Message:         "Test invalid schema: " + tc.name,
				RequestedSchema: tc.schema,
			})
			if err == nil {
				t.Errorf("expected error for invalid schema %q, got nil", tc.name)
				return
			}
			if code := errorCode(err); code != codeInvalidParams {
				t.Errorf("got error code %d, want %d (CodeInvalidParams)", code, codeInvalidParams)
			}
			if !strings.Contains(err.Error(), tc.expectedError) {
				t.Errorf("error message %q does not contain expected text %q", err.Error(), tc.expectedError)
			}
		})
	}
}

func TestElicitationProgressToken(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	params := &ElicitParams{
		Message: "Test progress token",
		Meta:    Meta{},
	}
	params.SetProgressToken("test-token")

	if token := params.GetProgressToken(); token != "test-token" {
		t.Errorf("got progress token %v, want %q", token, "test-token")
	}

	_, err = ss.Elicit(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
}

func TestElicitationCapabilityDeclaration(t *testing.T) {
	ctx := context.Background()

	t.Run("with_handler", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		// Client with ElicitationHandler should declare capability
		c := NewClient(testImpl, &ClientOptions{
			ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{Action: "cancel"}, nil
			},
		})

		s := NewServer(testImpl, nil)
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// The client should have declared elicitation capability during initialization
		// We can verify this worked by successfully making an elicitation call
		result, err := ss.Elicit(ctx, &ElicitParams{
			Message:         "Test capability",
			RequestedSchema: &jsonschema.Schema{Type: "object"},
		})
		if err != nil {
			t.Fatalf("elicitation should work when capability is declared, got error: %v", err)
		}
		if result.Action != "cancel" {
			t.Errorf("got action %q, want %q", result.Action, "cancel")
		}
	})

	t.Run("without_handler", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		// Client without ElicitationHandler should not declare capability
		c := NewClient(testImpl, &ClientOptions{
			CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
				return &CreateMessageResult{Model: "aModel", Content: &TextContent{}}, nil
			},
		})

		s := NewServer(testImpl, nil)
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// Elicitation should fail with UnsupportedMethod
		_, err = ss.Elicit(ctx, &ElicitParams{
			Message:         "This should fail",
			RequestedSchema: &jsonschema.Schema{Type: "object"},
		})

		if err == nil {
			t.Error("expected UnsupportedMethod error when no capability declared")
		}
		if code := errorCode(err); code != codeUnsupportedMethod {
			t.Errorf("got error code %d, want %d (CodeUnsupportedMethod)", code, codeUnsupportedMethod)
		}
	})
}

func TestKeepAliveFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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
	time.Sleep(30 * time.Millisecond)

	// simulate ping failure
	ss.Close()

	// Wait for keepalive to detect the failure and close the client
	// check periodically instead of just waiting
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		_, err = cs.CallTool(ctx, &CallToolParams{
			Name:      "greet",
			Arguments: map[string]any{"Name": "user"},
		})
		if errors.Is(err, ErrConnectionClosed) {
			return // Test passed
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Errorf("expected connection to be closed by keepalive, but it wasn't. Last error: %v", err)
}

func TestAddTool_DuplicateNoPanicAndNoDuplicate(t *testing.T) {
	// Adding the same tool pointer twice should not panic and should not
	// produce duplicates in the server's tool list.
	cs, _, cleanup := basicConnection(t, func(s *Server) {
		// Use two distinct Tool instances with the same name but different
		// descriptions to ensure the second replaces the first
		// This case was written specifically to reproduce a bug where duplicate tools where causing jsonschema errors
		t1 := &Tool{Name: "dup", Description: "first", InputSchema: &jsonschema.Schema{Type: "object"}}
		t2 := &Tool{Name: "dup", Description: "second", InputSchema: &jsonschema.Schema{Type: "object"}}
		s.AddTool(t1, nopHandler)
		s.AddTool(t2, nopHandler)
	})
	defer cleanup()

	ctx := context.Background()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var gotDesc string
	for _, tt := range res.Tools {
		if tt.Name == "dup" {
			count++
			gotDesc = tt.Description
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one 'dup' tool, got %d", count)
	}
	if gotDesc != "second" {
		t.Fatalf("expected replaced tool to have description %q, got %q", "second", gotDesc)
	}
}

func TestSynchronousNotifications(t *testing.T) {
	var toolsChanged atomic.Bool
	clientOpts := &ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, req *ToolListChangedRequest) {
			toolsChanged.Store(true)
		},
		CreateMessageHandler: func(ctx context.Context, req *CreateMessageRequest) (*CreateMessageResult, error) {
			if !toolsChanged.Load() {
				return nil, fmt.Errorf("didn't get a tools changed notification")
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
	cs, ss, cleanup := basicClientServerConnection(t, client, server, func(s *Server) {
		AddTool(s, &Tool{Name: "tool"}, func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
			if !rootsChanged.Load() {
				return nil, nil, fmt.Errorf("didn't get root change notification")
			}
			return new(CallToolResult), nil, nil
		})
	})
	defer cleanup()

	t.Run("from client", func(t *testing.T) {
		client.AddRoots(&Root{Name: "myroot", URI: "file://foo"})
		res, err := cs.CallTool(context.Background(), &CallToolParams{Name: "tool"})
		if err != nil {
			t.Fatalf("CallTool failed: %v", err)
		}
		if res.IsError {
			t.Errorf("tool error: %v", res.Content[0].(*TextContent).Text)
		}
	})

	t.Run("from server", func(t *testing.T) {
		server.RemoveTools("tool")
		if _, err := ss.CreateMessage(context.Background(), new(CreateMessageParams)); err != nil {
			t.Errorf("CreateMessage failed: %v", err)
		}
	})
}

func TestNoDistributedDeadlock(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cs.CallTool(ctx, &CallToolParams{Name: "tool1"}); err != nil {
		// should not deadlock
		t.Fatalf("CallTool failed: %v", err)
	}
}

var testImpl = &Implementation{Name: "test", Version: "v1.0.0"}

// This test checks that when we use pointer types for tools, we get the same
// schema as when using the non-pointer types. It is too much of a footgun for
// there to be a difference (see #199 and #200).
//
// If anyone asks, we can add an option that controls how pointers are treated.
func TestPointerArgEquivalence(t *testing.T) {
	type input struct {
		In string `json:",omitempty"`
	}
	type output struct {
		Out string
	}
	cs, _, cleanup := basicConnection(t, func(s *Server) {
		// Add two equivalent tools, one of which operates in the 'pointer' realm,
		// the other of which does not.
		//
		// We handle a few different types of results, to assert they behave the
		// same in all cases.
		AddTool(s, &Tool{Name: "pointer"}, func(_ context.Context, req *CallToolRequest, in *input) (*CallToolResult, *output, error) {
			switch in.In {
			case "":
				return nil, nil, fmt.Errorf("must provide input")
			case "nil":
				return nil, nil, nil
			case "empty":
				return &CallToolResult{}, nil, nil
			case "ok":
				return &CallToolResult{}, &output{Out: "foo"}, nil
			default:
				panic("unreachable")
			}
		})
		AddTool(s, &Tool{Name: "nonpointer"}, func(_ context.Context, req *CallToolRequest, in input) (*CallToolResult, output, error) {
			switch in.In {
			case "":
				return nil, output{}, fmt.Errorf("must provide input")
			case "nil":
				return nil, output{}, nil
			case "empty":
				return &CallToolResult{}, output{}, nil
			case "ok":
				return &CallToolResult{}, output{Out: "foo"}, nil
			default:
				panic("unreachable")
			}
		})
	})
	defer cleanup()

	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(tools.Tools), 2; got != want {
		t.Fatalf("got %d tools, want %d", got, want)
	}
	t0 := tools.Tools[0]
	t1 := tools.Tools[1]

	// First, check that the tool schemas don't differ.
	if diff := cmp.Diff(t0.InputSchema, t1.InputSchema); diff != "" {
		t.Errorf("input schemas do not match (-%s +%s):\n%s", t0.Name, t1.Name, diff)
	}
	if diff := cmp.Diff(t0.OutputSchema, t1.OutputSchema); diff != "" {
		t.Errorf("output schemas do not match (-%s +%s):\n%s", t0.Name, t1.Name, diff)
	}

	// Then, check that we handle empty input equivalently.
	for _, args := range []any{nil, struct{}{}} {
		r0, err := cs.CallTool(ctx, &CallToolParams{Name: t0.Name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		r1, err := cs.CallTool(ctx, &CallToolParams{Name: t1.Name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(r0, r1, ctrCmpOpts...); diff != "" {
			t.Errorf("CallTool(%v) with no arguments mismatch (-%s +%s):\n%s", args, t0.Name, t1.Name, diff)
		}
	}

	// Then, check that we handle different types of output equivalently.
	for _, in := range []string{"nil", "empty", "ok"} {
		t.Run(in, func(t *testing.T) {
			r0, err := cs.CallTool(ctx, &CallToolParams{Name: t0.Name, Arguments: input{In: in}})
			if err != nil {
				t.Fatal(err)
			}
			r1, err := cs.CallTool(ctx, &CallToolParams{Name: t1.Name, Arguments: input{In: in}})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(r0, r1, ctrCmpOpts...); diff != "" {
				t.Errorf("CallTool({\"In\": %q}) mismatch (-%s +%s):\n%s", in, t0.Name, t1.Name, diff)
			}
		})
	}
}

// ptr is a helper function to create pointers for schema constraints
func ptr[T any](v T) *T {
	return &v
}

func TestComplete(t *testing.T) {
	completionValues := []string{"python", "pytorch", "pyside"}

	serverOpts := &ServerOptions{
		CompletionHandler: func(_ context.Context, request *CompleteRequest) (*CompleteResult, error) {
			return &CompleteResult{
				Completion: CompletionResultDetails{
					Values: completionValues,
				},
			}, nil
		},
	}
	server := NewServer(testImpl, serverOpts)
	cs, _, cleanup := basicClientServerConnection(t, nil, server, func(s *Server) {})
	defer cleanup()

	result, err := cs.Complete(context.Background(), &CompleteParams{
		Argument: CompleteParamsArgument{
			Name:  "language",
			Value: "py",
		},
		Ref: &CompleteReference{
			Type: "ref/prompt",
			Name: "code_review",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(completionValues, result.Completion.Values); diff != "" {
		t.Errorf("Complete() mismatch (-want +got):\n%s", diff)
	}
}

// TestEmbeddedStructResponse performs a tool call to verify that a struct with
// an embedded pointer generates a correct, flattened JSON schema and that its
// response is validated successfully.
func TestEmbeddedStructResponse(t *testing.T) {
	type foo struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	// bar embeds foo
	type bar struct {
		*foo         // Embedded - should flatten in JSON
		Extra string `json:"extra"`
	}

	type response struct {
		Data bar `json:"data"`
	}

	// testTool demonstrates an embedded struct in its response.
	testTool := func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, response, error) {
		response := response{
			Data: bar{
				foo: &foo{
					ID:   "foo",
					Name: "Test Foo",
				},
				Extra: "additional data",
			},
		}
		return nil, response, nil
	}
	ctx := context.Background()
	clientTransport, serverTransport := NewInMemoryTransports()
	server := NewServer(&Implementation{Name: "testServer", Version: "v1.0.0"}, nil)
	AddTool(server, &Tool{
		Name: "test_embedded_struct",
	}, testTool)

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := NewClient(&Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	_, err = clientSession.CallTool(ctx, &CallToolParams{
		Name: "test_embedded_struct",
	})
	if err != nil {
		t.Errorf("CallTool() failed: %v", err)
	}
}

func TestToolErrorMiddleware(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	AddTool(s, &Tool{
		Name:        "greet",
		Description: "say hi",
	}, sayHi)
	AddTool(s, &Tool{Name: "fail", InputSchema: &jsonschema.Schema{Type: "object"}},
		func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, any, error) {
			return nil, nil, errTestFailure
		})

	var middleErr error
	s.AddReceivingMiddleware(func(h MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			res, err := h(ctx, method, req)
			if err == nil {
				if ctr, ok := res.(*CallToolResult); ok {
					middleErr = ctr.getError()
				}
			}
			return res, err
		}
	})
	_, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(&Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	_, err = clientSession.CallTool(ctx, &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"Name": "al"},
	})
	if err != nil {
		t.Errorf("CallTool() failed: %v", err)
	}
	if middleErr != nil {
		t.Errorf("middleware got error %v, want nil", middleErr)
	}
	res, err := clientSession.CallTool(ctx, &CallToolParams{
		Name: "fail",
	})
	if err != nil {
		t.Errorf("CallTool() failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("want error, got none")
	}
	// Clients can't see the error, because it isn't marshaled.
	if err := res.getError(); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if middleErr != errTestFailure {
		t.Errorf("middleware got err %v, want errTestFailure", middleErr)
	}
}

var ctrCmpOpts = []cmp.Option{cmp.AllowUnexported(CallToolResult{})}
