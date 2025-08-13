// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestStreamableTransports(t *testing.T) {
	// This test checks that the streamable server and client transports can
	// communicate.

	ctx := context.Background()

	for _, useJSON := range []bool{false, true} {
		t.Run(fmt.Sprintf("JSONResponse=%v", useJSON), func(t *testing.T) {
			// Create a server with some simple tools.
			server := NewServer(testImpl, nil)
			AddTool(server, &Tool{Name: "greet", Description: "say hi"}, sayHi)
			// The "hang" tool checks that context cancellation is propagated.
			// It hangs until the context is cancelled.
			var (
				start     = make(chan struct{})
				cancelled = make(chan struct{}, 1) // don't block the request
			)
			hang := func(ctx context.Context, req *ServerRequest[*CallToolParams]) (*CallToolResult, error) {
				start <- struct{}{}
				select {
				case <-ctx.Done():
					cancelled <- struct{}{}
				case <-time.After(5 * time.Second):
					return nil, nil
				}
				return nil, nil
			}
			AddTool(server, &Tool{Name: "hang"}, hang)
			AddTool(server, &Tool{Name: "sample"}, func(ctx context.Context, req *ServerRequest[*CallToolParams]) (*CallToolResult, error) {
				// Test that we can make sampling requests during tool handling.
				//
				// Try this on both the request context and a background context, so
				// that messages may be delivered on either the POST or GET connection.
				for _, ctx := range map[string]context.Context{
					"request context":    ctx,
					"background context": context.Background(),
				} {
					res, err := req.Session.CreateMessage(ctx, &CreateMessageParams{})
					if err != nil {
						return nil, err
					}
					if g, w := res.Model, "aModel"; g != w {
						return nil, fmt.Errorf("got %q, want %q", g, w)
					}
				}
				return &CallToolResultFor[any]{}, nil
			})

			// Start an httptest.Server with the StreamableHTTPHandler, wrapped in a
			// cookie-checking middleware.
			handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, &StreamableHTTPOptions{
				jsonResponse: useJSON,
			})

			var (
				headerMu   sync.Mutex
				lastHeader http.Header
			)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				headerMu.Lock()
				lastHeader = r.Header
				headerMu.Unlock()
				cookie, err := r.Cookie("test-cookie")
				if err != nil {
					t.Errorf("missing cookie: %v", err)
				} else if cookie.Value != "test-value" {
					t.Errorf("got cookie %q, want %q", cookie.Value, "test-value")
				}
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			// Create a client and connect it to the server using our StreamableClientTransport.
			// Check that all requests honor a custom client.
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(httpServer.URL)
			if err != nil {
				t.Fatal(err)
			}
			jar.SetCookies(u, []*http.Cookie{{Name: "test-cookie", Value: "test-value"}})
			httpClient := &http.Client{Jar: jar}
			transport := &StreamableClientTransport{
				Endpoint:   httpServer.URL,
				HTTPClient: httpClient,
			}
			client := NewClient(testImpl, &ClientOptions{
				CreateMessageHandler: func(context.Context, *ClientRequest[*CreateMessageParams]) (*CreateMessageResult, error) {
					return &CreateMessageResult{Model: "aModel", Content: &TextContent{}}, nil
				},
			})
			session, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatalf("client.Connect() failed: %v", err)
			}
			defer session.Close()
			sid := session.ID()
			if sid == "" {
				t.Error("empty session ID")
			}
			if g, w := session.mcpConn.(*streamableClientConn).initializedResult.ProtocolVersion, latestProtocolVersion; g != w {
				t.Fatalf("got protocol version %q, want %q", g, w)
			}

			// Verify the behavior of various tools.

			// The "greet" tool should just work.
			params := &CallToolParams{
				Name:      "greet",
				Arguments: map[string]any{"name": "foo"},
			}
			got, err := session.CallTool(ctx, params)
			if err != nil {
				t.Fatalf("CallTool() failed: %v", err)
			}
			if g := session.ID(); g != sid {
				t.Errorf("session ID: got %q, want %q", g, sid)
			}
			if g, w := lastHeader.Get(protocolVersionHeader), latestProtocolVersion; g != w {
				t.Errorf("got protocol version header %q, want %q", g, w)
			}
			want := &CallToolResult{
				Content: []Content{&TextContent{Text: "hi foo"}},
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("CallTool() returned unexpected content (-want +got):\n%s", diff)
			}

			// The "hang" tool should be cancellable.
			ctx2, cancel := context.WithCancel(context.Background())
			go session.CallTool(ctx2, &CallToolParams{Name: "hang"})
			<-start
			cancel()
			select {
			case <-cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("timeout waiting for cancellation")
			}

			// The "sampling" tool should be able to issue sampling requests during
			// tool operation.
			result, err := session.CallTool(ctx, &CallToolParams{
				Name:      "sample",
				Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("tool failed: %s", result.Content[0].(*TextContent).Text)
			}
		})
	}
}

// TestClientReplay verifies that the client can recover from a mid-stream
// network failure and receive replayed messages (if replay is configured). It
// uses a proxy that is killed and restarted to simulate a recoverable network
// outage.
func TestClientReplay(t *testing.T) {
	for _, test := range []clientReplayTest{
		{"default", nil, true},
		{"no retries", &StreamableReconnectOptions{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			testClientReplay(t, test)
		})
	}
}

type clientReplayTest struct {
	name          string
	options       *StreamableReconnectOptions
	wantRecovered bool
}

func testClientReplay(t *testing.T, test clientReplayTest) {
	notifications := make(chan string)
	// Configure the real MCP server.
	server := NewServer(testImpl, nil)

	// Use a channel to synchronize the server's message sending with the test's
	// proxy-killing action.
	serverReadyToKillProxy := make(chan struct{})
	serverClosed := make(chan struct{})
	server.AddTool(&Tool{Name: "multiMessageTool", InputSchema: &jsonschema.Schema{}},
		func(ctx context.Context, req *ServerRequest[*CallToolParamsFor[map[string]any]]) (*CallToolResult, error) {
			// Send one message to the request context, and another to a background
			// context (which will end up on the hanging GET).

			bgCtx := context.Background()
			req.Session.NotifyProgress(ctx, &ProgressNotificationParams{Message: "msg1"})
			req.Session.NotifyProgress(bgCtx, &ProgressNotificationParams{Message: "msg2"})

			// Signal the test that it can now kill the proxy.
			close(serverReadyToKillProxy)
			<-serverClosed

			// These messages should be queued for replay by the server after
			// the client's connection drops.
			req.Session.NotifyProgress(ctx, &ProgressNotificationParams{Message: "msg3"})
			req.Session.NotifyProgress(bgCtx, &ProgressNotificationParams{Message: "msg4"})
			return new(CallToolResult), nil
		})

	realServer := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil))
	defer realServer.Close()
	realServerURL, err := url.Parse(realServer.URL)
	if err != nil {
		t.Fatalf("Failed to parse real server URL: %v", err)
	}

	// Configure a proxy that sits between the client and the real server.
	proxyHandler := httputil.NewSingleHostReverseProxy(realServerURL)
	proxy := httptest.NewServer(proxyHandler)
	proxyAddr := proxy.Listener.Addr().String() // Get the address to restart it later.

	// Configure the client to connect to the proxy with default options.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewClient(testImpl, &ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, req *ClientRequest[*ProgressNotificationParams]) {
			notifications <- req.Params.Message
		},
	})
	clientSession, err := client.Connect(ctx, &StreamableClientTransport{
		Endpoint:         proxy.URL,
		ReconnectOptions: test.options,
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer clientSession.Close()

	var (
		wg      sync.WaitGroup
		callErr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, callErr = clientSession.CallTool(ctx, &CallToolParams{Name: "multiMessageTool"})
	}()

	select {
	case <-serverReadyToKillProxy:
		// Server has sent the first two messages and is paused.
	case <-ctx.Done():
		t.Fatalf("Context timed out before server was ready to kill proxy")
	}

	// We should always get the first two notifications.
	msgs := readNotifications(t, ctx, notifications, 2)
	sort.Strings(msgs) // notifications may arrive in either order
	want := []string{"msg1", "msg2"}
	if diff := cmp.Diff(want, msgs); diff != "" {
		t.Errorf("Recovered notifications mismatch (-want +got):\n%s", diff)
	}

	// Simulate a total network failure by closing the proxy.
	t.Log("--- Killing proxy to simulate network failure ---")
	proxy.CloseClientConnections()
	proxy.Close()
	close(serverClosed)

	// Simulate network recovery by restarting the proxy on the same address.
	t.Logf("--- Restarting proxy on %s ---", proxyAddr)
	listener, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("Failed to listen on proxy address: %v", err)
	}

	restartedProxy := &http.Server{Handler: proxyHandler}
	go restartedProxy.Serve(listener)
	defer restartedProxy.Close()

	wg.Wait()

	if test.wantRecovered {
		// If we've recovered, we should get all 4 notifications and the tool call
		// should have succeeded.
		msgs := readNotifications(t, ctx, notifications, 2)
		sort.Strings(msgs)
		want := []string{"msg3", "msg4"}
		if diff := cmp.Diff(want, msgs); diff != "" {
			t.Errorf("Recovered notifications mismatch (-want +got):\n%s", diff)
		}
		if callErr != nil {
			t.Errorf("CallTool failed unexpectedly: %v", err)
		}
	} else {
		// Otherwise, the call should fail.
		if callErr == nil {
			t.Errorf("CallTool succeeded unexpectedly")
		}
	}
}

// TestServerInitiatedSSE verifies that the persistent SSE connection remains
// open and can receive server-initiated events.
func TestServerInitiatedSSE(t *testing.T) {
	notifications := make(chan string)
	server := NewServer(testImpl, nil)

	httpServer := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewClient(testImpl, &ClientOptions{
		ToolListChangedHandler: func(context.Context, *ClientRequest[*ToolListChangedParams]) {
			notifications <- "toolListChanged"
		},
	})
	clientSession, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer clientSession.Close()
	server.AddTool(&Tool{Name: "testTool", InputSchema: &jsonschema.Schema{}},
		func(context.Context, *ServerRequest[*CallToolParamsFor[map[string]any]]) (*CallToolResult, error) {
			return &CallToolResult{}, nil
		})
	receivedNotifications := readNotifications(t, ctx, notifications, 1)
	wantReceived := []string{"toolListChanged"}
	if diff := cmp.Diff(wantReceived, receivedNotifications); diff != "" {
		t.Errorf("Received notifications mismatch (-want +got):\n%s", diff)
	}
}

// Helper to read a specific number of notifications.
func readNotifications(t *testing.T, ctx context.Context, notifications chan string, count int) []string {
	t.Helper()
	var collectedNotifications []string
	for {
		select {
		case n := <-notifications:
			collectedNotifications = append(collectedNotifications, n)
			if len(collectedNotifications) == count {
				return collectedNotifications
			}
		case <-ctx.Done():
			if len(collectedNotifications) != count {
				t.Fatalf("readProgressNotifications(): did not receive expected notifications, got %d, want %d", len(collectedNotifications), count)
			}
			return collectedNotifications
		}
	}
}

func TestStreamableServerTransport(t *testing.T) {
	// This test checks detailed behavior of the streamable server transport, by
	// faking the behavior of a streamable client using a sequence of HTTP
	// requests.

	// JSON-RPC message constructors.
	req := func(id int64, method string, params any) *jsonrpc.Request {
		r := &jsonrpc.Request{
			Method: method,
			Params: mustMarshal(t, params),
		}
		if id > 0 {
			r.ID = jsonrpc2.Int64ID(id)
		}
		return r
	}
	resp := func(id int64, result any, err error) *jsonrpc.Response {
		return &jsonrpc.Response{
			ID:     jsonrpc2.Int64ID(id),
			Result: mustMarshal(t, result),
			Error:  err,
		}
	}

	// Predefined steps, to avoid repetition below.
	initReq := req(1, methodInitialize, &InitializeParams{})
	initResp := resp(1, &InitializeResult{
		Capabilities: &serverCapabilities{
			Logging: &loggingCapabilities{},
			Tools:   &toolCapabilities{ListChanged: true},
		},
		ProtocolVersion: latestProtocolVersion,
		ServerInfo:      &Implementation{Name: "testServer", Version: "v1.0.0"},
	}, nil)
	initializedMsg := req(0, notificationInitialized, &InitializedParams{})
	initialize := streamableRequest{
		method:         "POST",
		messages:       []jsonrpc.Message{initReq},
		wantStatusCode: http.StatusOK,
		wantMessages:   []jsonrpc.Message{initResp},
	}
	initialized := streamableRequest{
		method:         "POST",
		messages:       []jsonrpc.Message{initializedMsg},
		wantStatusCode: http.StatusAccepted,
	}

	tests := []struct {
		name  string
		tool  func(*testing.T, context.Context, *ServerSession)
		steps []streamableRequest
	}{
		{
			name: "basic",
			steps: []streamableRequest{
				initialize,
				initialized,
				{
					method:         "POST",
					messages:       []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages:   []jsonrpc.Message{resp(2, &CallToolResult{}, nil)},
				},
			},
		},
		{
			name: "accept headers",
			steps: []streamableRequest{
				initialize,
				initialized,
				// Test various accept headers.
				{
					method:         "POST",
					accept:         []string{"text/plain", "application/*"},
					messages:       []jsonrpc.Message{req(3, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusBadRequest, // missing text/event-stream
				},
				{
					method:         "POST",
					accept:         []string{"text/event-stream"},
					messages:       []jsonrpc.Message{req(3, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusBadRequest, // missing application/json
				},
				{
					method:         "POST",
					accept:         []string{"text/plain", "*/*"},
					messages:       []jsonrpc.Message{req(4, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages:   []jsonrpc.Message{resp(4, &CallToolResult{}, nil)},
				},
				{
					method:         "POST",
					accept:         []string{"text/*, application/*"},
					messages:       []jsonrpc.Message{req(4, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages:   []jsonrpc.Message{resp(4, &CallToolResult{}, nil)},
				},
			},
		},
		{
			name: "tool notification",
			tool: func(t *testing.T, ctx context.Context, ss *ServerSession) {
				// Send an arbitrary notification.
				if err := ss.NotifyProgress(ctx, &ProgressNotificationParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
			},
			steps: []streamableRequest{
				initialize,
				initialized,
				{
					method: "POST",
					messages: []jsonrpc.Message{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					wantStatusCode: http.StatusOK,
					wantMessages: []jsonrpc.Message{
						req(0, "notifications/progress", &ProgressNotificationParams{}),
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "tool upcall",
			tool: func(t *testing.T, ctx context.Context, ss *ServerSession) {
				// Make an arbitrary call.
				if _, err := ss.ListRoots(ctx, &ListRootsParams{}); err != nil {
					t.Errorf("Call failed: %v", err)
				}
			},
			steps: []streamableRequest{
				initialize,
				initialized,
				{
					method:    "POST",
					onRequest: 1,
					messages: []jsonrpc.Message{
						resp(1, &ListRootsResult{}, nil),
					},
					wantStatusCode: http.StatusAccepted,
				},
				{
					method: "POST",
					messages: []jsonrpc.Message{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					wantStatusCode: http.StatusOK,
					wantMessages: []jsonrpc.Message{
						req(1, "roots/list", &ListRootsParams{}),
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "background",
			tool: func(t *testing.T, ctx context.Context, ss *ServerSession) {
				// Perform operations on a background context, and ensure the client
				// receives it.
				ctx = context.Background()
				if err := ss.NotifyProgress(ctx, &ProgressNotificationParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
				// TODO(rfindley): finish implementing logging.
				// if err := ss.LoggingMessage(ctx, &LoggingMessageParams{}); err != nil {
				// 	t.Errorf("Logging failed: %v", err)
				// }
				if _, err := ss.ListRoots(ctx, &ListRootsParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
			},
			steps: []streamableRequest{
				initialize,
				initialized,
				{
					method:    "POST",
					onRequest: 1,
					messages: []jsonrpc.Message{
						resp(1, &ListRootsResult{}, nil),
					},
					wantStatusCode: http.StatusAccepted,
				},
				{
					method:         "GET",
					async:          true,
					wantStatusCode: http.StatusOK,
					closeAfter:     2,
					wantMessages: []jsonrpc.Message{
						req(0, "notifications/progress", &ProgressNotificationParams{}),
						req(1, "roots/list", &ListRootsParams{}),
					},
				},
				{
					method: "POST",
					messages: []jsonrpc.Message{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					wantStatusCode: http.StatusOK,
					wantMessages: []jsonrpc.Message{
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "errors",
			steps: []streamableRequest{
				{
					method:         "PUT",
					wantStatusCode: http.StatusMethodNotAllowed,
				},
				{
					method:         "DELETE",
					wantStatusCode: http.StatusBadRequest,
				},
				{
					method:         "POST",
					messages:       []jsonrpc.Message{req(1, "notamethod", nil)},
					wantStatusCode: http.StatusBadRequest, // notamethod is an invalid method
				},
				{
					method:         "POST",
					messages:       []jsonrpc.Message{req(0, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusBadRequest, // tools/call must have an ID
				},
				{
					method:         "POST",
					messages:       []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages: []jsonrpc.Message{resp(2, nil, &jsonrpc2.WireError{
						Message: `method "tools/call" is invalid during session initialization`,
					})},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Create a server containing a single tool, which runs the test tool
			// behavior, if any.
			server := NewServer(&Implementation{Name: "testServer", Version: "v1.0.0"}, nil)
			AddTool(server, &Tool{Name: "tool"}, func(ctx context.Context, req *ServerRequest[*CallToolParamsFor[any]]) (*CallToolResultFor[any], error) {
				if test.tool != nil {
					test.tool(t, ctx, req.Session)
				}
				return &CallToolResultFor[any]{}, nil
			})

			// Start the streamable handler.
			handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
			defer handler.closeAll()

			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			// blocks records request blocks by jsonrpc. ID.
			//
			// When an OnRequest step is encountered, it waits on the corresponding
			// block. When a request with that ID is received, the block is closed.
			var mu sync.Mutex
			blocks := make(map[int64]chan struct{})
			for _, step := range test.steps {
				if step.onRequest > 0 {
					blocks[step.onRequest] = make(chan struct{})
				}
			}

			// signal when all synchronous requests have executed, so we can fail
			// async requests that are blocked.
			syncRequestsDone := make(chan struct{})

			// To avoid complicated accounting for session ID, just set the first
			// non-empty session ID from a response.
			var sessionID atomic.Value
			sessionID.Store("")

			// doStep executes a single step.
			doStep := func(t *testing.T, step streamableRequest) {
				if step.onRequest > 0 {
					// Block the step until we've received the server->client request.
					mu.Lock()
					block := blocks[step.onRequest]
					mu.Unlock()
					select {
					case <-block:
					case <-syncRequestsDone:
						t.Errorf("after all sync requests are complete, request still blocked on %d", step.onRequest)
						return
					}
				}

				// Collect messages received during this request, unblock other steps
				// when requests are received.
				var got []jsonrpc.Message
				out := make(chan jsonrpc.Message)
				// Cancel the step if we encounter a request that isn't going to be
				// handled.
				ctx, cancel := context.WithCancel(context.Background())

				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()

					for m := range out {
						if req, ok := m.(*jsonrpc.Request); ok && req.ID.IsValid() {
							// Encountered a server->client request. We should have a
							// response queued. Otherwise, we may deadlock.
							mu.Lock()
							if block, ok := blocks[req.ID.Raw().(int64)]; ok {
								close(block)
							} else {
								t.Errorf("no queued response for %v", req.ID)
								cancel()
							}
							mu.Unlock()
						}
						got = append(got, m)
						if step.closeAfter > 0 && len(got) == step.closeAfter {
							cancel()
						}
					}
				}()

				gotSessionID, gotStatusCode, err := step.do(ctx, httpServer.URL, sessionID.Load().(string), out)

				// Don't fail on cancelled requests: error (if any) is handled
				// elsewhere.
				if err != nil && ctx.Err() == nil {
					t.Fatal(err)
				}

				if gotStatusCode != step.wantStatusCode {
					t.Errorf("got status %d, want %d", gotStatusCode, step.wantStatusCode)
				}
				wg.Wait()

				transform := cmpopts.AcyclicTransformer("jsonrpcid", func(id jsonrpc.ID) any { return id.Raw() })
				if diff := cmp.Diff(step.wantMessages, got, transform); diff != "" {
					t.Errorf("received unexpected messages (-want +got):\n%s", diff)
				}
				sessionID.CompareAndSwap("", gotSessionID)
			}

			var wg sync.WaitGroup
			for _, step := range test.steps {
				if step.async || step.onRequest > 0 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						doStep(t, step)
					}()
				} else {
					doStep(t, step)
				}
			}

			// Fail any blocked responses if they weren't needed by a synchronous
			// request.
			close(syncRequestsDone)

			wg.Wait()
		})
	}
}

// A streamableRequest describes a single streamable HTTP request, consisting
// of a request payload and expected response.
type streamableRequest struct {
	// If onRequest is > 0, this step only executes after a request with the
	// given ID is received.
	//
	// All onRequest steps must occur before the step that creates the request.
	//
	// To avoid tests hanging when there's a bug, it's expected that this
	// request is received in the course of a *synchronous* request to the
	// server (otherwise, we wouldn't be able to terminate the test without
	// analyzing a dependency graph).
	onRequest int64
	// If set, async causes the step to run asynchronously to other steps.
	// Redundant with OnRequest: all OnRequest steps are asynchronous.
	async bool

	// Request attributes
	method   string            // HTTP request method
	accept   []string          // if non-empty, the Accept header to use; otherwise the default header is used
	messages []jsonrpc.Message // messages to send

	closeAfter     int               // if nonzero, close after receiving this many messages
	wantStatusCode int               // expected status code
	wantMessages   []jsonrpc.Message // expected messages to receive
}

// streamingRequest makes a request to the given streamable server with the
// given url, sessionID, and method.
//
// If provided, the in messages are encoded in the request body. A single
// message is encoded as a JSON object. Multiple messages are batched as a JSON
// array.
//
// Any received messages are sent to the out channel, which is closed when the
// request completes.
//
// Returns the sessionID and http status code from the response. If an error is
// returned, sessionID and status code may still be set if the error occurs
// after the response headers have been received.
func (s streamableRequest) do(ctx context.Context, serverURL, sessionID string, out chan<- jsonrpc.Message) (string, int, error) {
	defer close(out)

	var body []byte
	if len(s.messages) == 1 {
		data, err := jsonrpc2.EncodeMessage(s.messages[0])
		if err != nil {
			return "", 0, fmt.Errorf("encoding message: %w", err)
		}
		body = data
	} else {
		var rawMsgs []json.RawMessage
		for _, msg := range s.messages {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				return "", 0, fmt.Errorf("encoding message: %w", err)
			}
			rawMsgs = append(rawMsgs, data)
		}
		data, err := json.Marshal(rawMsgs)
		if err != nil {
			return "", 0, fmt.Errorf("marshaling batch: %w", err)
		}
		body = data
	}

	req, err := http.NewRequestWithContext(ctx, s.method, serverURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("creating request: %w", err)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	req.Header.Set("Content-Type", "application/json")
	if len(s.accept) > 0 {
		for _, accept := range s.accept {
			req.Header.Add("Accept", accept)
		}
	} else {
		req.Header.Add("Accept", "application/json, text/event-stream")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	newSessionID := resp.Header.Get("Mcp-Session-Id")

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		for evt, err := range scanEvents(resp.Body) {
			if err != nil {
				return newSessionID, resp.StatusCode, fmt.Errorf("reading events: %v", err)
			}
			// TODO(rfindley): do we need to check evt.name?
			// Does the MCP spec say anything about this?
			msg, err := jsonrpc2.DecodeMessage(evt.Data)
			if err != nil {
				return newSessionID, resp.StatusCode, fmt.Errorf("decoding message: %w", err)
			}
			out <- msg
		}
	} else if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return newSessionID, resp.StatusCode, fmt.Errorf("reading json body: %w", err)
		}
		msg, err := jsonrpc2.DecodeMessage(data)
		if err != nil {
			return newSessionID, resp.StatusCode, fmt.Errorf("decoding message: %w", err)
		}
		out <- msg
	}

	return newSessionID, resp.StatusCode, nil
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	if v == nil {
		return nil
	}
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStreamableClientTransportApplicationJSON(t *testing.T) {
	// Test handling of application/json responses.
	ctx := context.Background()
	resp := func(id int64, result any, err error) *jsonrpc.Response {
		return &jsonrpc.Response{
			ID:     jsonrpc2.Int64ID(id),
			Result: mustMarshal(t, result),
			Error:  err,
		}
	}
	initResult := &InitializeResult{
		Capabilities: &serverCapabilities{
			Logging: &loggingCapabilities{},
			Tools:   &toolCapabilities{ListChanged: true},
		},
		ProtocolVersion: latestProtocolVersion,
		ServerInfo:      &Implementation{Name: "testServer", Version: "v1.0.0"},
	}
	initResp := resp(1, initResult, nil)

	serverHandler := func(w http.ResponseWriter, r *http.Request) {
		data, err := jsonrpc2.EncodeMessage(initResp)
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "123")
		w.Write(data)
	}

	httpServer := httptest.NewServer(http.HandlerFunc(serverHandler))
	defer httpServer.Close()

	transport := &StreamableClientTransport{Endpoint: httpServer.URL}
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()
	if diff := cmp.Diff(initResult, session.state.InitializeResult); diff != "" {
		t.Errorf("mismatch (-want, +got):\n%s", diff)
	}
}

func TestEventID(t *testing.T) {
	tests := []struct {
		sid StreamID
		idx int
	}{
		{"0", 0},
		{"0", 1},
		{"1", 0},
		{"1", 1},
		{"", 1},
		{"1234", 5678},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%s_%d", test.sid, test.idx), func(t *testing.T) {
			eventID := formatEventID(test.sid, test.idx)
			gotSID, gotIdx, ok := parseEventID(eventID)
			if !ok {
				t.Fatalf("parseEventID(%q) failed, want ok", eventID)
			}
			if gotSID != test.sid || gotIdx != test.idx {
				t.Errorf("parseEventID(%q) = %s, %d, want %s, %d", eventID, gotSID, gotIdx, test.sid, test.idx)
			}
		})
	}

	invalid := []string{
		"",
		"_",
		"1_",
		"1_a",
		"1_-1",
	}

	for _, eventID := range invalid {
		t.Run(fmt.Sprintf("invalid_%q", eventID), func(t *testing.T) {
			if _, _, ok := parseEventID(eventID); ok {
				t.Errorf("parseEventID(%q) succeeded, want failure", eventID)
			}
		})
	}
}

func TestStreamableStateless(t *testing.T) {
	// Test stateless mode behavior
	ctx := context.Background()

	// This version of sayHi doesn't make a ping request (we can't respond to
	// that request from our client).
	sayHi := func(ctx context.Context, req *ServerRequest[*CallToolParamsFor[hiParams]]) (*CallToolResultFor[any], error) {
		return &CallToolResultFor[any]{Content: []Content{&TextContent{Text: "hi " + req.Params.Arguments.Name}}}, nil
	}
	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "greet", Description: "say hi"}, sayHi)

	// Test stateless mode.
	handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{
		GetSessionID: func() string { return "" },
	})
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	checkRequest := func(body string) {
		// Verify we can call tools/list directly without initialization in stateless mode
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		// Verify that no session ID header is returned in stateless mode
		if sessionID := resp.Header.Get(sessionIDHeader); sessionID != "" {
			t.Errorf("%s = %s, want no session ID header", sessionIDHeader, sessionID)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Status code = %d; want successful response", resp.StatusCode)
		}

		var events []Event
		for event, err := range scanEvents(resp.Body) {
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		if len(events) != 1 {
			t.Fatalf("got %d SSE events, want 1; events: %v", len(events), events)
		}
		msg, err := jsonrpc.DecodeMessage(events[0].Data)
		if err != nil {
			t.Fatal(err)
		}
		jsonResp, ok := msg.(*jsonrpc.Response)
		if !ok {
			t.Errorf("event is %T, want response", jsonResp)
		}
		if jsonResp.Error != nil {
			t.Errorf("request failed: %v", jsonResp.Error)
		}
	}

	checkRequest(`{"jsonrpc":"2.0","method":"tools/list","id":1,"params":{}}`)

	// Verify we can make another request without session ID
	checkRequest(`{"jsonrpc":"2.0","method":"tools/call","id":2,"params":{"name":"greet","arguments":{"name":"World"}}}`)
}
