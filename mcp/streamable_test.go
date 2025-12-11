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
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestStreamableTransports(t *testing.T) {
	// This test checks that the streamable server and client transports can
	// communicate.

	ctx := context.Background()

	tests := []struct {
		useJSON bool
		replay  bool
	}{
		{false, false},
		{false, true},
		{true, false},
		{true, true},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("JSONResponse=%v;replay=%v", test.useJSON, test.replay), func(t *testing.T) {
			// Create a server with some simple tools.
			server := NewServer(testImpl, nil)
			AddTool(server, &Tool{Name: "greet", Description: "say hi"}, sayHi)
			// The "hang" tool checks that context cancellation is propagated.
			// It hangs until the context is cancelled.
			var (
				start     = make(chan struct{})
				cancelled = make(chan struct{}, 1) // don't block the request
			)
			hang := func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
				start <- struct{}{}
				select {
				case <-ctx.Done():
					cancelled <- struct{}{}
				case <-time.After(5 * time.Second):
					return nil, nil, nil
				}
				return nil, nil, nil
			}
			AddTool(server, &Tool{Name: "hang"}, hang)
			// We use sampling to test server->client requests, both before and after
			// the related client->server request completes.
			sampleDone := make(chan struct{})
			var sampleWG sync.WaitGroup
			AddTool(server, &Tool{Name: "sample"}, func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, any, error) {
				type testCase struct {
					label       string
					ctx         context.Context
					wantSuccess bool
				}
				testSample := func(tc testCase) {
					res, err := req.Session.CreateMessage(tc.ctx, &CreateMessageParams{})
					if gotSuccess := err == nil; gotSuccess != tc.wantSuccess {
						t.Errorf("%s: CreateMessage success=%v, want %v", tc.label, gotSuccess, tc.wantSuccess)
					}
					if err != nil {
						return
					}
					if g, w := res.Model, "aModel"; g != w {
						t.Errorf("%s: got model %q, want %q", tc.label, g, w)
					}
				}
				// Test that we can make sampling requests during tool handling.
				//
				// Try this on both the request context and a background context, so
				// that messages may be delivered on either the POST or GET connection.
				for _, test := range []testCase{
					{"request context", ctx, true},
					{"background context", context.Background(), true},
				} {
					testSample(test)
				}
				// Now, spin off a goroutine that runs after the sampling request, to
				// check behavior when the client request has completed.
				sampleWG.Add(1)
				go func() {
					defer sampleWG.Done()
					<-sampleDone
					// Test that sampling requests in the tool context fail outside of
					// tool handling, but succeed on the background context.
					for _, test := range []testCase{
						{"request context", ctx, false},
						{"background context", context.Background(), true},
					} {
						testSample(test)
					}
				}()
				return &CallToolResult{}, nil, nil
			})

			// Start an httptest.Server with the StreamableHTTPHandler, wrapped in a
			// cookie-checking middleware.
			opts := &StreamableHTTPOptions{
				JSONResponse: test.useJSON,
			}
			if test.replay {
				opts.EventStore = NewMemoryEventStore(nil)
			}
			handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, opts)

			var (
				headerMu   sync.Mutex
				lastHeader http.Header
			)
			httpServer := httptest.NewServer(mustNotPanic(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			})))
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
				CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
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
				t.Fatalf("empty session ID")
			}
			if g, w := session.mcpConn.(*streamableClientConn).initializedResult.ProtocolVersion, latestProtocolVersion; g != w {
				t.Fatalf("got protocol version %q, want %q", g, w)
			}

			// Verify the behavior of various tools.

			// The "greet" tool should just work.
			params := &CallToolParams{
				Name:      "greet",
				Arguments: map[string]any{"Name": "foo"},
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
			if diff := cmp.Diff(want, got, ctrCmpOpts...); diff != "" {
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

			// The "sampling" tool checks the validity of server->client requests
			// both within and without the tool context.
			result, err := session.CallTool(ctx, &CallToolParams{
				Name:      "sample",
				Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			// Run the out-of-band checks.
			close(sampleDone)
			sampleWG.Wait()

			if result.IsError {
				t.Fatalf("tool failed: %s", result.Content[0].(*TextContent).Text)
			}
		})
	}
}

func TestStreamableConcurrentHandling(t *testing.T) {
	// This test checks that the streamable server and client transports can
	// communicate.
	type count struct {
		Count int
	}

	var mu sync.Mutex
	counts := make(map[string]int)

	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "inc"}, func(ctx context.Context, req *CallToolRequest, _ any) (*CallToolResult, count, error) {
		id := req.Session.ID()
		mu.Lock()
		defer mu.Unlock()
		c := counts[id]
		counts[id] = c + 1
		return nil, count{c}, nil
	})
	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	ctx := context.Background()
	client := NewClient(testImpl, nil)
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			clientSession, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
			if err != nil {
				t.Errorf("Connect failed: %v", err)
				return
			}
			defer clientSession.Close()
			for i := range 10 {
				res, err := clientSession.CallTool(ctx, &CallToolParams{Name: "inc"})
				if err != nil {
					t.Errorf("CallTool failed: %v", err)
					return
				}
				if got := int(res.StructuredContent.(map[string]any)["Count"].(float64)); got != i {
					t.Errorf("got count %d, want %d", got, i)
				}
			}
		}()
	}
	wg.Wait()
}

func TestStreamableServerShutdown(t *testing.T) {
	ctx := context.Background()

	// This test checks that closing the streamable HTTP server actually results
	// in client session termination, provided one of following holds:
	//  1. The server is stateful, and therefore the hanging GET fails the connection.
	//  2. The server is stateless, and the client uses a KeepAlive.
	tests := []struct {
		name                 string
		stateless, keepalive bool
	}{
		{"stateful", false, false},
		{"stateless with keepalive", true, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(testImpl, nil)
			// Add a tool, just so we can check things are working.
			AddTool(server, &Tool{Name: "greet"}, sayHi)

			handler := NewStreamableHTTPHandler(
				func(req *http.Request) *Server { return server },
				&StreamableHTTPOptions{Stateless: test.stateless})

			// When we shut down the server, we need to explicitly close ongoing
			// connections. Otherwise, the hanging GET may never terminate.
			httpServer := httptest.NewUnstartedServer(handler)
			httpServer.Config.RegisterOnShutdown(func() {
				for session := range server.Sessions() {
					session.Close()
				}
			})
			httpServer.Start()
			defer httpServer.Close()

			// Connect and run a tool.
			var opts ClientOptions
			if test.keepalive {
				opts.KeepAlive = 50 * time.Millisecond
			}
			client := NewClient(testImpl, &opts)
			clientSession, err := client.Connect(ctx, &StreamableClientTransport{
				Endpoint:   httpServer.URL,
				MaxRetries: -1, // avoid slow tests during exponential retries
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer clientSession.Close()

			params := &CallToolParams{
				Name:      "greet",
				Arguments: map[string]any{"Name": "foo"},
			}
			// Verify that we can call a tool.
			if _, err := clientSession.CallTool(ctx, params); err != nil {
				t.Fatalf("CallTool() failed: %v", err)
			}

			// Shut down the server. Sessions should terminate.
			go func() {
				if err := httpServer.Config.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("closing http server: %v", err)
				}
			}()

			// Wait may return an error (after all, the connection failed), but it
			// should not hang.
			t.Log("Client waiting")
			_ = clientSession.Wait()
		})
	}
}

// TestClientReplay verifies that the client can recover from a mid-stream
// network failure and receive replayed messages (if replay is configured). It
// uses a proxy that is killed and restarted to simulate a recoverable network
// outage.
//
// TODO: Until we have a way to clean up abandoned sessions, this test will leak goroutines (see #499)
func TestClientReplay(t *testing.T) {
	for _, test := range []clientReplayTest{
		{"default", 0, true},
		{"no retries", -1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			testClientReplay(t, test)
		})
	}
}

type clientReplayTest struct {
	name          string
	maxRetries    int
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
	AddTool(server, &Tool{Name: "multiMessageTool", InputSchema: &jsonschema.Schema{Type: "object"}},
		func(ctx context.Context, req *CallToolRequest, args map[string]any) (*CallToolResult, any, error) {
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
			return new(CallToolResult), nil, nil
		})

	realServer := httptest.NewServer(mustNotPanic(t, NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{
		EventStore: NewMemoryEventStore(nil), // necessary for replay
	})))
	t.Cleanup(func() {
		t.Log("Closing real HTTP server")
		realServer.Close()
	})
	realServerURL, err := url.Parse(realServer.URL)
	if err != nil {
		t.Fatalf("Failed to parse real server URL: %v", err)
	}

	// Configure a proxy that sits between the client and the real server.
	proxyHandler := httputil.NewSingleHostReverseProxy(realServerURL)
	// note: don't use mustNotPanic here as the proxy WILL panic when killed.
	proxy := httptest.NewServer(proxyHandler)
	proxyAddr := proxy.Listener.Addr().String() // Get the address to restart it later.

	// Configure the client to connect to the proxy with default options.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewClient(testImpl, &ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, req *ProgressNotificationClientRequest) {
			notifications <- req.Params.Message
		},
	})
	clientSession, err := client.Connect(ctx, &StreamableClientTransport{
		Endpoint:   proxy.URL,
		MaxRetries: test.maxRetries,
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	t.Cleanup(func() {
		t.Log("Closing clientSession")
		clientSession.Close()
	})

	toolCallResult := make(chan error, 1)
	go func() {
		_, callErr := clientSession.CallTool(ctx, &CallToolParams{Name: "multiMessageTool"})
		toolCallResult <- callErr
	}()

	select {
	case <-serverReadyToKillProxy:
		t.Log("Server has sent the first two messages and is paused.")
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
	t.Cleanup(func() { restartedProxy.Close() })

	callErr := <-toolCallResult

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

func TestStreamableServerDisconnect(t *testing.T) {
	server := NewServer(testImpl, nil)

	// Test that client replayability allows the server to terminate incoming
	// requests immediately, and have the client replay them.

	// testStream exercises stream resumption by interleaving stream termination
	// with progress notifications.
	testStream := func(ctx context.Context, session *ServerSession, extra *RequestExtra) {
		// Close the stream before the first message. We should have sent an
		// initial priming message already, so the client will be able to replay
		extra.CloseSSEStream(CloseSSEStreamArgs{RetryAfter: 10 * time.Millisecond})
		session.NotifyProgress(ctx, &ProgressNotificationParams{Message: "msg1"})
		time.Sleep(20 * time.Millisecond)
		extra.CloseSSEStream(CloseSSEStreamArgs{RetryAfter: 10 * time.Millisecond}) // Closing twice should still be supported.
		session.NotifyProgress(ctx, &ProgressNotificationParams{Message: "msg2"})
	}

	AddTool(server, &Tool{Name: "disconnect"},
		func(ctx context.Context, req *CallToolRequest, args map[string]any) (*CallToolResult, map[string]any, error) {
			testStream(ctx, req.Session, req.Extra)
			return new(CallToolResult), nil, nil
		})

	server.AddPrompt(&Prompt{Name: "disconnect"}, func(ctx context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
		testStream(ctx, req.Session, req.Extra)
		return nil, nil
	})

	tests := []struct {
		name   string
		doCall func(context.Context, *ClientSession) error
	}{
		{
			"tool",
			func(ctx context.Context, cs *ClientSession) error {
				_, err := cs.CallTool(ctx, &CallToolParams{Name: "disconnect"})
				return err
			},
		},
		{
			"prompt",
			func(ctx context.Context, cs *ClientSession) error {
				_, err := cs.GetPrompt(ctx, &GetPromptParams{Name: "disconnect"})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notifications := make(chan string, 2)
			handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{
				EventStore: NewMemoryEventStore(nil), // support replayability
			})
			httpServer := httptest.NewServer(mustNotPanic(t, handler))
			defer httpServer.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := NewClient(testImpl, &ClientOptions{
				ProgressNotificationHandler: func(ctx context.Context, req *ProgressNotificationClientRequest) {
					notifications <- req.Params.Message
				},
			})
			clientSession, err := client.Connect(ctx, &StreamableClientTransport{
				Endpoint: httpServer.URL,
			}, &ClientSessionOptions{protocolVersion: protocolVersion20251125})
			if err != nil {
				t.Fatalf("client.Connect() failed: %v", err)
			}
			defer clientSession.Close()

			if err = test.doCall(ctx, clientSession); err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}

			got := readNotifications(t, ctx, notifications, 2)
			want := []string{"msg1", "msg2"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("got unexpected notifications (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServerTransportCleanup(t *testing.T) {
	nClient := 3

	var mu sync.Mutex
	var id int = -1 // session id starting from "0", "1", "2"...
	chans := make(map[string]chan struct{}, nClient)

	server := NewServer(testImpl, &ServerOptions{
		KeepAlive: 10 * time.Millisecond,
		GetSessionID: func() string {
			mu.Lock()
			defer mu.Unlock()
			id++
			if id == nClient {
				t.Errorf("creating more than %v session", nClient)
			}
			chans[fmt.Sprint(id)] = make(chan struct{}, 1)
			return fmt.Sprint(id)
		},
	})

	handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil)
	handler.onTransportDeletion = func(sessionID string) {
		chans[sessionID] <- struct{}{}
	}

	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Spin up clients connect to the same server but refuse to ping request.
	for range nClient {
		client := NewClient(testImpl, nil)
		pingMiddleware := func(next MethodHandler) MethodHandler {
			return func(
				ctx context.Context,
				method string,
				req Request,
			) (Result, error) {
				if method == "ping" {
					return &emptyResult{}, errors.New("ping error")
				}
				return next(ctx, method, req)
			}
		}
		client.AddReceivingMiddleware(pingMiddleware)
		clientSession, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
		if err != nil {
			t.Fatalf("client.Connect() failed: %v", err)
		}
		t.Cleanup(func() { _ = clientSession.Close() })
	}

	for _, ch := range chans {
		select {
		case <-ctx.Done():
			t.Errorf("did not capture transport deletion event from all session in 10 seconds")
		case <-ch:
			t.Log("Received session transport deletion signal")
		}
	}

	handler.mu.Lock()
	if len(handler.sessions) != 0 {
		t.Errorf("want empty transports map, find %v entries from handler's transports map", len(handler.sessions))
	}
	handler.mu.Unlock()
}

// TestServerInitiatedSSE verifies that the persistent SSE connection remains
// open and can receive server-initiated events.
func TestServerInitiatedSSE(t *testing.T) {
	notifications := make(chan string)
	server := NewServer(testImpl, nil)

	httpServer := httptest.NewServer(mustNotPanic(t, NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil)))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewClient(testImpl, &ClientOptions{
		ToolListChangedHandler: func(context.Context, *ToolListChangedRequest) {
			notifications <- "toolListChanged"
		},
	})
	clientSession, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer clientSession.Close()
	AddTool(server, &Tool{Name: "testTool", InputSchema: &jsonschema.Schema{Type: "object"}},
		func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, any, error) {
			return &CallToolResult{}, nil, nil
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

// JSON-RPC message constructors.
func req(id int64, method string, params any) *jsonrpc.Request {
	r := &jsonrpc.Request{
		Method: method,
		Params: mustMarshal(params),
	}
	if id > 0 {
		r.ID = jsonrpc2.Int64ID(id)
	}
	return r
}

func resp(id int64, result any, err error) *jsonrpc.Response {
	return &jsonrpc.Response{
		ID:     jsonrpc2.Int64ID(id),
		Result: mustMarshal(result),
		Error:  err,
	}
}

func TestStreamableServerTransport(t *testing.T) {
	// This test checks detailed behavior of the streamable server transport, by
	// faking the behavior of a streamable client using a sequence of HTTP
	// requests.

	// Predefined steps, to avoid repetition below.
	initReq := req(1, methodInitialize, &InitializeParams{ProtocolVersion: protocolVersion20250618})
	initResp := resp(1, &InitializeResult{
		Capabilities: &ServerCapabilities{
			Logging: &LoggingCapabilities{},
			Tools:   &ToolCapabilities{ListChanged: true},
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
		wantSessionID:  true,
	}
	initialized := streamableRequest{
		method:         "POST",
		messages:       []jsonrpc.Message{initializedMsg},
		wantStatusCode: http.StatusAccepted,
	}

	// Protocol version 2025-11-25 variants, for testing prime/close events (SEP-1699).
	initReq20251125 := req(1, methodInitialize, &InitializeParams{ProtocolVersion: protocolVersion20251125})
	initResp20251125 := resp(1, &InitializeResult{
		Capabilities: &ServerCapabilities{
			Logging: &LoggingCapabilities{},
			Tools:   &ToolCapabilities{ListChanged: true},
		},
		ProtocolVersion: protocolVersion20251125,
		ServerInfo:      &Implementation{Name: "testServer", Version: "v1.0.0"},
	}, nil)
	initialize20251125 := streamableRequest{
		method:         "POST",
		messages:       []jsonrpc.Message{initReq20251125},
		wantStatusCode: http.StatusOK,
		wantMessages:   []jsonrpc.Message{initResp20251125},
		wantSessionID:  true,
	}
	initialized20251125 := streamableRequest{
		method:         "POST",
		headers:        http.Header{protocolVersionHeader: {protocolVersion20251125}},
		messages:       []jsonrpc.Message{initializedMsg},
		wantStatusCode: http.StatusAccepted,
	}

	tests := []struct {
		name         string
		replay       bool                                                // if set, use a MemoryEventStore to enable replay
		tool         func(*testing.T, context.Context, *CallToolRequest) // if set, called during execution
		requests     []streamableRequest
		wantSessions int // number of sessions expected after the test
	}{
		{
			name: "basic",
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method:         "POST",
					messages:       []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages:   []jsonrpc.Message{resp(2, &CallToolResult{Content: []Content{}}, nil)},
				},
			},
			wantSessions: 1,
		},
		{
			name: "uninitialized",
			requests: []streamableRequest{
				{
					method:             "POST",
					messages:           []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:     http.StatusOK,
					wantBodyContaining: "invalid during session initialization",
				},
			},
			wantSessions: 0,
		},
		{
			name: "accept headers",
			requests: []streamableRequest{
				initialize,
				initialized,
				// Test various accept headers.
				{
					method:         "POST",
					headers:        http.Header{"Accept": {"text/plain", "application/*"}},
					messages:       []jsonrpc.Message{req(3, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusBadRequest, // missing text/event-stream
				},
				{
					method:         "POST",
					headers:        http.Header{"Accept": {"text/event-stream"}},
					messages:       []jsonrpc.Message{req(3, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusBadRequest, // missing application/json
				},
				{
					method:         "POST",
					headers:        http.Header{"Accept": {"text/plain", "*/*"}},
					messages:       []jsonrpc.Message{req(4, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages:   []jsonrpc.Message{resp(4, &CallToolResult{Content: []Content{}}, nil)},
				},
				{
					method:         "POST",
					headers:        http.Header{"Accept": {"text/*, application/*"}},
					messages:       []jsonrpc.Message{req(4, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode: http.StatusOK,
					wantMessages:   []jsonrpc.Message{resp(4, &CallToolResult{Content: []Content{}}, nil)},
				},
			},
			wantSessions: 1,
		},
		{
			name: "protocol version headers",
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method:             "POST",
					headers:            http.Header{"mcp-protocol-version": {"2025-01-01"}}, // an invalid protocol version
					messages:           []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:     http.StatusBadRequest,
					wantBodyContaining: "2025-03-26", // a supported version
					wantSessionID:      false,        // could be true, but shouldn't matter
				},
			},
			wantSessions: 1,
		},
		{
			name: "batch rejected on 2025-06-18",
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method: "POST",
					// Explicitly set the protocol version header
					headers: http.Header{"MCP-Protocol-Version": {"2025-06-18"}},
					// Two messages => batch. Expect reject.
					messages: []jsonrpc.Message{
						req(101, "tools/call", &CallToolParams{Name: "tool"}),
						req(102, "tools/call", &CallToolParams{Name: "tool"}),
					},
					wantStatusCode:     http.StatusBadRequest,
					wantBodyContaining: "batch",
				},
			},
			wantSessions: 1,
		},
		{
			name: "batch accepted on 2025-03-26",
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method:  "POST",
					headers: http.Header{"MCP-Protocol-Version": {"2025-03-26"}},
					// Two messages => batch. Expect OK with two responses in order.
					messages: []jsonrpc.Message{
						// Note: only include one request here, because responses are not
						// necessarily sorted.
						req(201, "tools/call", &CallToolParams{Name: "tool"}),
						req(0, "notifications/roots/list_changed", &RootsListChangedParams{}),
					},
					wantStatusCode: http.StatusOK,
					wantMessages: []jsonrpc.Message{
						resp(201, &CallToolResult{Content: []Content{}}, nil),
					},
				},
			},
			wantSessions: 1,
		},
		{
			name: "tool notification",
			tool: func(t *testing.T, ctx context.Context, req *CallToolRequest) {
				// Send an arbitrary notification.
				if err := req.Session.NotifyProgress(ctx, &ProgressNotificationParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
			},
			requests: []streamableRequest{
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
						resp(2, &CallToolResult{Content: []Content{}}, nil),
					},
				},
			},
			wantSessions: 1,
		},
		{
			name: "tool upcall",
			tool: func(t *testing.T, ctx context.Context, req *CallToolRequest) {
				// Make an arbitrary call.
				if _, err := req.Session.ListRoots(ctx, &ListRootsParams{}); err != nil {
					t.Errorf("Call failed: %v", err)
				}
			},
			requests: []streamableRequest{
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
						resp(2, &CallToolResult{Content: []Content{}}, nil),
					},
				},
			},
			wantSessions: 1,
		},
		{
			name: "background",
			// Enabling replay is necessary here because the standalone "GET" request
			// is fully asynronous. Replay is needed to guarantee message delivery.
			//
			// TODO(rfindley): this should no longer be necessary.
			replay: true,
			tool: func(t *testing.T, _ context.Context, req *CallToolRequest) {
				// Perform operations on a background context, and ensure the client
				// receives it.
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()

				if err := req.Session.NotifyProgress(ctx, &ProgressNotificationParams{}); err != nil {
					t.Errorf("Notify failed: %v", err)
				}
				// TODO(rfindley): finish implementing logging.
				// if err := ss.LoggingMessage(ctx, &LoggingMessageParams{}); err != nil {
				// 	t.Errorf("Logging failed: %v", err)
				// }
				if _, err := req.Session.ListRoots(ctx, &ListRootsParams{}); err != nil {
					t.Errorf("ListRoots failed: %v", err)
				}
			},
			requests: []streamableRequest{
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
						resp(2, &CallToolResult{Content: []Content{}}, nil),
					},
				},
				{
					method:         "DELETE",
					wantStatusCode: http.StatusNoContent,
					// Delete request expects 204 No Content with empty body. So override
					// the default "accept: application/json, text/event-stream" header.
					headers: map[string][]string{"Accept": nil},
				},
			},
			wantSessions: 0, // session deleted
		},
		{
			name:   "no priming message on old protocol",
			replay: true,
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method:                "POST",
					messages:              []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:        http.StatusOK,
					wantMessages:          []jsonrpc.Message{resp(2, &CallToolResult{Content: []Content{}}, nil)},
					wantBodyNotContaining: "prime",
				},
			},
			wantSessions: 1,
		},
		{
			name:   "no close message on old protocol",
			replay: true,
			tool: func(t *testing.T, _ context.Context, req *CallToolRequest) {
				req.Extra.CloseSSEStream(CloseSSEStreamArgs{RetryAfter: time.Millisecond})
			},
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method:                "POST",
					messages:              []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:        http.StatusOK,
					wantMessages:          []jsonrpc.Message{resp(2, &CallToolResult{Content: []Content{}}, nil)},
					wantBodyNotContaining: "close",
				},
			},
			wantSessions: 1,
		},
		{
			name:   "priming message on 2025-11-25",
			replay: true,
			requests: []streamableRequest{
				initialize20251125,
				initialized20251125,
				{
					method:             "POST",
					headers:            http.Header{protocolVersionHeader: {protocolVersion20251125}},
					messages:           []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:     http.StatusOK,
					wantMessages:       []jsonrpc.Message{resp(2, &CallToolResult{Content: []Content{}}, nil)},
					wantBodyContaining: "prime",
				},
			},
			wantSessions: 1,
		},
		{
			name:   "close message on 2025-11-25",
			replay: true,
			tool: func(t *testing.T, _ context.Context, req *CallToolRequest) {
				req.Extra.CloseSSEStream(CloseSSEStreamArgs{RetryAfter: time.Millisecond})
			},
			requests: []streamableRequest{
				initialize20251125,
				initialized20251125,
				{
					method:                "POST",
					headers:               http.Header{protocolVersionHeader: {protocolVersion20251125}},
					messages:              []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:        http.StatusOK,
					wantMessages:          []jsonrpc.Message{resp(2, &CallToolResult{Content: []Content{}}, nil)},
					wantBodyContaining:    "close",
					wantBodyNotContaining: "result",
				},
			},
			wantSessions: 1,
		},
		{
			name:   "no close message",
			replay: true,
			tool: func(t *testing.T, _ context.Context, req *CallToolRequest) {
				req.Extra.CloseSSEStream(CloseSSEStreamArgs{})
			},
			requests: []streamableRequest{
				initialize,
				initialized,
				{
					method:                "POST",
					messages:              []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					wantStatusCode:        http.StatusOK,
					wantMessages:          []jsonrpc.Message{resp(2, &CallToolResult{Content: []Content{}}, nil)},
					wantBodyNotContaining: "close",
				},
			},
			wantSessions: 1,
		},
		{
			name: "errors",
			requests: []streamableRequest{
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
					wantMessages: []jsonrpc.Message{resp(2, nil, &jsonrpc.Error{
						Message: `method "tools/call" is invalid during session initialization`,
					})},
				},
			},
			wantSessions: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Create a server containing a single tool, which runs the test tool
			// behavior, if any.
			server := NewServer(&Implementation{Name: "testServer", Version: "v1.0.0"}, nil)
			server.AddTool(
				&Tool{Name: "tool", InputSchema: &jsonschema.Schema{Type: "object"}},
				func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
					if test.tool != nil {
						test.tool(t, ctx, req)
					}
					return &CallToolResult{}, nil
				})

			opts := &StreamableHTTPOptions{}
			if test.replay {
				opts.EventStore = NewMemoryEventStore(nil)
			}
			// Start the streamable handler.
			handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, opts)
			defer handler.closeAll()

			testStreamableHandler(t, handler, test.requests)
			if got := len(slices.Collect(server.Sessions())); got != test.wantSessions {
				t.Errorf("after test, got %d sessions, want %d", got, test.wantSessions)
			}
		})
	}
}

func testStreamableHandler(t *testing.T, handler http.Handler, requests []streamableRequest) {
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	// blocks records request blocks by jsonrpc. ID.
	//
	// When an OnRequest step is encountered, it waits on the corresponding
	// block. When a request with that ID is received, the block is closed.
	var mu sync.Mutex
	blocks := make(map[int64]chan struct{})
	for _, req := range requests {
		if req.onRequest > 0 {
			blocks[req.onRequest] = make(chan struct{})
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
	doStep := func(t *testing.T, i int, request streamableRequest) {
		if request.onRequest > 0 {
			// Block the step until we've received the server->client request.
			mu.Lock()
			block := blocks[request.onRequest]
			mu.Unlock()
			select {
			case <-block:
			case <-syncRequestsDone:
				t.Errorf("after all sync requests are complete, request still blocked on %d", request.onRequest)
				return
			}
		}

		// Collect messages received during this request, unblock other steps
		// when requests are received.
		var got []jsonrpc.Message
		out := make(chan jsonrpc.Message)
		// Cancel the step if we encounter a request that isn't going to be
		// handled.
		//
		// Also, add a timeout (hopefully generous).
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			for m := range out {
				if req, ok := m.(*jsonrpc.Request); ok && req.IsCall() {
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
				if request.closeAfter > 0 && len(got) == request.closeAfter {
					cancel()
				}
			}
		}()

		gotSessionID, gotStatusCode, gotBody, err := request.do(ctx, httpServer.URL, sessionID.Load().(string), out)

		// Don't fail on cancelled requests: error (if any) is handled
		// elsewhere.
		if err != nil && ctx.Err() == nil {
			t.Fatal(err)
		}

		if gotStatusCode != request.wantStatusCode {
			t.Errorf("request #%d: got status %d, want %d", i, gotStatusCode, request.wantStatusCode)
		}
		if got := gotSessionID != ""; got != request.wantSessionID {
			t.Errorf("request #%d: got session id: %t, want %t", i, got, request.wantSessionID)
		}
		wg.Wait()

		if request.wantBodyContaining != "" || request.wantBodyNotContaining != "" {
			body := string(gotBody)
			if request.wantBodyContaining != "" && !strings.Contains(body, request.wantBodyContaining) {
				t.Errorf("body does not contain %q:\n%s", request.wantBodyContaining, body)
			}
			if request.wantBodyNotContaining != "" && strings.Contains(body, request.wantBodyNotContaining) {
				t.Errorf("body contains %q:\n%s", request.wantBodyNotContaining, body)
			}
		} else {
			transform := cmpopts.AcyclicTransformer("jsonrpcid", func(id jsonrpc.ID) any { return id.Raw() })
			if diff := cmp.Diff(request.wantMessages, got, transform); diff != "" {
				t.Errorf("request #%d: received unexpected messages (-want +got):\n%s", i, diff)
			}
		}
		sessionID.CompareAndSwap("", gotSessionID)
	}

	var wg sync.WaitGroup
	for i, request := range requests {
		if request.async || request.onRequest > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				doStep(t, i, request)
			}()
		} else {
			doStep(t, i, request)
		}
	}

	// Fail any blocked responses if they weren't needed by a synchronous
	// request.
	close(syncRequestsDone)

	wg.Wait()
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
	method   string            // HTTP request method (required)
	headers  http.Header       // additional headers to set, overlaid on top of the default headers
	messages []jsonrpc.Message // messages to send

	closeAfter            int               // if nonzero, close after receiving this many messages
	wantStatusCode        int               // expected status code
	wantBodyContaining    string            // if set, expect the response body to contain this text; overrides wantMessages
	wantBodyNotContaining string            // if set, a negative assertion on the body; overrides wantMessages
	wantMessages          []jsonrpc.Message // expected messages to receive; ignored if wantBodyContaining is set
	wantSessionID         bool              // whether or not a session ID is expected in the response
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
func (s streamableRequest) do(ctx context.Context, serverURL, sessionID string, out chan<- jsonrpc.Message) (string, int, []byte, error) {
	defer close(out)

	var body []byte
	if len(s.messages) == 1 {
		data, err := jsonrpc2.EncodeMessage(s.messages[0])
		if err != nil {
			return "", 0, nil, fmt.Errorf("encoding message: %w", err)
		}
		body = data
	} else {
		var rawMsgs []json.RawMessage
		for _, msg := range s.messages {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				return "", 0, nil, fmt.Errorf("encoding message: %w", err)
			}
			rawMsgs = append(rawMsgs, data)
		}
		data, err := json.Marshal(rawMsgs)
		if err != nil {
			return "", 0, nil, fmt.Errorf("marshaling batch: %w", err)
		}
		body = data
	}

	req, err := http.NewRequestWithContext(ctx, s.method, serverURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, nil, fmt.Errorf("creating request: %w", err)
	}
	if sessionID != "" {
		req.Header.Set(sessionIDHeader, sessionID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	maps.Copy(req.Header, s.headers)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, nil, fmt.Errorf("request failed: %v", err)
	}
	defer resp.Body.Close()

	newSessionID := resp.Header.Get(sessionIDHeader)

	contentType := resp.Header.Get("Content-Type")
	var respBody []byte
	if strings.HasPrefix(contentType, "text/event-stream") {
		r := readerInto{resp.Body, new(bytes.Buffer)}
		for evt, err := range scanEvents(r) {
			if err != nil {
				return newSessionID, resp.StatusCode, nil, fmt.Errorf("reading events: %v", err)
			}
			if evt.Name == "" || evt.Name == "message" { // ordinary message
				// TODO(rfindley): do we need to check evt.name?
				// Does the MCP spec say anything about this?
				msg, err := jsonrpc2.DecodeMessage(evt.Data)
				if err != nil {
					return newSessionID, resp.StatusCode, nil, fmt.Errorf("decoding message: %w", err)
				}
				out <- msg
			}
		}
		respBody = r.w.Bytes()
	} else if strings.HasPrefix(contentType, "application/json") {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return newSessionID, resp.StatusCode, nil, fmt.Errorf("reading json body: %w", err)
		}
		respBody = data
		msg, err := jsonrpc2.DecodeMessage(data)
		if err != nil {
			return newSessionID, resp.StatusCode, nil, fmt.Errorf("decoding message: %w", err)
		}
		out <- msg
	} else {
		respBody, err = io.ReadAll(resp.Body)
		if err != nil {
			return newSessionID, resp.StatusCode, nil, fmt.Errorf("reading response: %v", err)
		}
	}

	return newSessionID, resp.StatusCode, respBody, nil
}

// readerInto is an io.Reader that writes any bytes read from r into w.
type readerInto struct {
	r io.Reader
	w *bytes.Buffer
}

// Read implements io.Reader.
func (r readerInto) Read(p []byte) (n int, err error) {
	n, err = r.r.Read(p)
	if err == nil || err == io.EOF {
		n2, err2 := r.w.Write(p[:n])
		if err2 != nil {
			return n, fmt.Errorf("failed to write: %v", err)
		}
		if n2 != n {
			return n, fmt.Errorf("short write: %d != %d", n2, n)
		}
	}
	return n, err
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func TestEventID(t *testing.T) {
	tests := []struct {
		sid string
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
	initReq := req(1, methodInitialize, &InitializeParams{})
	initResp := resp(1, &InitializeResult{
		Capabilities: &ServerCapabilities{
			Logging: &LoggingCapabilities{},
			Tools:   &ToolCapabilities{ListChanged: true},
		},
		ProtocolVersion: latestProtocolVersion,
		ServerInfo:      &Implementation{Name: "test", Version: "v1.0.0"},
	}, nil)
	// This version of sayHi expects
	// that request from our client).
	sayHi := func(ctx context.Context, req *CallToolRequest, args hiParams) (*CallToolResult, any, error) {
		if err := req.Session.Ping(ctx, nil); err == nil {
			// ping should fail, but not break the connection
			t.Errorf("ping succeeded unexpectedly")
		}
		return &CallToolResult{Content: []Content{&TextContent{Text: "hi " + args.Name}}}, nil, nil
	}

	requests := []streamableRequest{
		{
			method:         "POST",
			messages:       []jsonrpc.Message{initReq},
			wantStatusCode: http.StatusOK,
			wantMessages:   []jsonrpc.Message{initResp},
			wantSessionID:  false, // sessionless
		},
		{
			method:             "POST",
			wantStatusCode:     http.StatusOK,
			messages:           []jsonrpc.Message{req(1, "tools/list", struct{}{})},
			wantBodyContaining: "greet",
		},
		{
			method:         "GET",
			wantStatusCode: http.StatusMethodNotAllowed,
		},
		{
			method:         "POST",
			wantStatusCode: http.StatusOK,
			messages: []jsonrpc.Message{
				req(2, "tools/call", &CallToolParams{Name: "greet", Arguments: hiParams{Name: "World"}}),
			},
			wantMessages: []jsonrpc.Message{
				resp(2, &CallToolResult{
					Content: []Content{&TextContent{Text: "hi World"}},
				}, nil),
			},
		},
		{
			method:         "POST",
			wantStatusCode: http.StatusOK,
			messages: []jsonrpc.Message{
				req(2, "tools/call", &CallToolParams{Name: "greet", Arguments: hiParams{Name: "foo"}}),
			},
			wantMessages: []jsonrpc.Message{
				resp(2, &CallToolResult{
					Content: []Content{&TextContent{Text: "hi foo"}},
				}, nil),
			},
		},
	}

	testClientCompatibility := func(t *testing.T, handler http.Handler) {
		ctx := context.Background()
		httpServer := httptest.NewServer(mustNotPanic(t, handler))
		defer httpServer.Close()
		cs, err := NewClient(testImpl, nil).Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cs.Close() })
		res, err := cs.CallTool(ctx, &CallToolParams{Name: "greet", Arguments: hiParams{Name: "bar"}})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := textContent(t, res), "hi bar"; got != want {
			t.Errorf("Result = %q, want %q", got, want)
		}
	}

	sessionlessHandler := NewStreamableHTTPHandler(func(*http.Request) *Server {
		// Return a stateless server which never assigns a session ID.
		server := NewServer(testImpl, &ServerOptions{
			GetSessionID: func() string { return "" },
		})
		AddTool(server, &Tool{Name: "greet", Description: "say hi"}, sayHi)
		return server
	}, &StreamableHTTPOptions{
		Stateless: true,
	})

	// First, test the "sessionless" stateless mode, where there is no session ID.
	t.Run("sessionless", func(t *testing.T) {
		testStreamableHandler(t, sessionlessHandler, requests)
		testClientCompatibility(t, sessionlessHandler)
	})

	// Next, test the default stateless mode, where session IDs are permitted.
	//
	// This can be used by tools to look up application state preserved across
	// subsequent requests.
	requests[0].wantSessionID = true // now expect a session ID for initialize
	statelessHandler := NewStreamableHTTPHandler(func(*http.Request) *Server {
		// Return a server with default options which should assign a random session ID.
		server := NewServer(testImpl, nil)
		AddTool(server, &Tool{Name: "greet", Description: "say hi"}, sayHi)
		return server
	}, &StreamableHTTPOptions{
		Stateless: true,
	})
	t.Run("stateless", func(t *testing.T) {
		testStreamableHandler(t, statelessHandler, requests)
		testClientCompatibility(t, sessionlessHandler)
	})
}

func textContent(t *testing.T, res *CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T, want *TextContent", res.Content[0])
	}
	return text.Text
}

func TestTokenInfo(t *testing.T) {
	oldAuth := testAuth.Load()
	defer testAuth.Store(oldAuth)
	testAuth.Store(true)
	ctx := context.Background()

	// Create a server with a tool that returns TokenInfo.
	tokenInfo := func(ctx context.Context, req *CallToolRequest, _ struct{}) (*CallToolResult, any, error) {
		return &CallToolResult{Content: []Content{&TextContent{Text: fmt.Sprintf("%v", req.Extra.TokenInfo)}}}, nil, nil
	}
	server := NewServer(testImpl, nil)
	AddTool(server, &Tool{Name: "tokenInfo", Description: "return token info"}, tokenInfo)

	streamHandler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	verifier := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{
			Scopes: []string{"scope"},
			// Expiration is far, far in the future.
			Expiration: time.Date(5000, 1, 2, 3, 4, 5, 0, time.UTC),
		}, nil
	}
	handler := auth.RequireBearerToken(verifier, nil)(streamHandler)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	transport := &StreamableClientTransport{Endpoint: httpServer.URL}
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &CallToolParams{Name: "tokenInfo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) == 0 {
		t.Fatal("missing content")
	}
	tc, ok := res.Content[0].(*TextContent)
	if !ok {
		t.Fatal("not TextContent")
	}
	if g, w := tc.Text, "&{[scope] 5000-01-02 03:04:05 +0000 UTC  map[]}"; g != w {
		t.Errorf("got %q, want %q", g, w)
	}
}

func TestSessionHijackingPrevention(t *testing.T) {
	// This test verifies that sessions bound to a user ID cannot be accessed
	// by a different user (session hijacking prevention).
	ctx := context.Background()

	server := NewServer(testImpl, nil)
	streamHandler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)

	// Use the bearer token directly as the user ID. This simulates how a real
	// verifier might extract a user ID from a JWT "sub" claim or introspection.
	verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		return &auth.TokenInfo{
			Scopes:     []string{"scope"},
			UserID:     token,
			Expiration: time.Date(5000, 1, 2, 3, 4, 5, 0, time.UTC),
		}, nil
	}
	handler := auth.RequireBearerToken(verifier, nil)(streamHandler)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	// Helper to send a JSON-RPC request as a given user.
	doRequest := func(msg jsonrpc.Message, sessionID, userID string) *http.Response {
		t.Helper()
		data, _ := jsonrpc2.EncodeMessage(msg)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+userID)
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		return resp
	}

	// Create a session as user1.
	initReq := &jsonrpc.Request{Method: "initialize", ID: jsonrpc2.Int64ID(1)}
	initReq.Params, _ = json.Marshal(&InitializeParams{
		ProtocolVersion: protocolVersion20250618,
		ClientInfo:      &Implementation{Name: "test", Version: "1.0"},
	})
	resp := doRequest(initReq, "", "user1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("initialize failed with status %d: %s", resp.StatusCode, body)
	}
	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("no session ID in response")
	}

	pingReq := &jsonrpc.Request{Method: "ping", ID: jsonrpc2.Int64ID(2)}
	pingReq.Params, _ = json.Marshal(&PingParams{})

	// Try to access the session as user2 - should fail.
	resp2 := doRequest(pingReq, sessionID, "user2")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("expected status %d for user mismatch, got %d: %s", http.StatusForbidden, resp2.StatusCode, body)
	}

	// Access as original user1 should succeed.
	resp3 := doRequest(pingReq, sessionID, "user1")
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp3.Body)
		t.Errorf("expected status %d for matching user, got %d: %s", http.StatusOK, resp3.StatusCode, body)
	}
}

func TestStreamableGET(t *testing.T) {
	// This test checks the fix for problematic behavior described in #410:
	// Hanging GET headers should be written immediately, even if there are no
	// messages.
	server := NewServer(testImpl, nil)

	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	newReq := func(method string, msg jsonrpc.Message) *http.Request {
		var body io.Reader
		if msg != nil {
			data, err := jsonrpc2.EncodeMessage(msg)
			if err != nil {
				t.Fatal(err)
			}
			body = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, httpServer.URL, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", "application/json, text/event-stream")
		if msg != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req
	}

	get1 := newReq(http.MethodGet, nil)
	resp, err := http.DefaultClient.Do(get1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resp.StatusCode, http.StatusMethodNotAllowed; got != want {
		t.Errorf("initial GET: got status %d, want %d", got, want)
	}
	defer resp.Body.Close()

	post1 := newReq(http.MethodPost, req(1, methodInitialize, &InitializeParams{}))
	resp, err = http.DefaultClient.Do(post1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		t.Errorf("initialize POST: got status %d, want %d; body:\n%s", got, want, string(body))
	}

	sessionID := resp.Header.Get(sessionIDHeader)
	if sessionID == "" {
		t.Fatalf("initialized missing session ID")
	}

	get2 := newReq("GET", nil)
	get2.Header.Set(sessionIDHeader, sessionID)
	resp, err = http.DefaultClient.Do(get2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("GET with session ID: got status %d, want %d", got, want)
	}

	t.Log("Sending final DELETE request to close session and release resources")
	del := newReq("DELETE", nil)
	del.Header.Set(sessionIDHeader, sessionID)
	resp, err = http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusNoContent; got != want {
		t.Errorf("DELETE with session ID: got status %d, want %d", got, want)
	}
}

func TestStreamableClientContextPropagation(t *testing.T) {
	type contextKey string
	const testKey = contextKey("test-key")
	const testValue = "test-value"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx2 := context.WithValue(ctx, testKey, testValue)

	server := httptest.NewServer(mustNotPanic(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "POST":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "test-session")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"test","version":"1.0"}}}`))
		case "GET":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
		case "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	})))
	defer server.Close()

	transport := &StreamableClientTransport{Endpoint: server.URL}
	conn, err := transport.Connect(ctx2)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer conn.Close()

	streamableConn, ok := conn.(*streamableClientConn)
	if !ok {
		t.Fatalf("Expected *streamableClientConn, got %T", conn)
	}

	if got := streamableConn.ctx.Value(testKey); got != testValue {
		t.Errorf("Context value not propagated: got %v, want %v", got, testValue)
	}

	if streamableConn.ctx.Done() == nil {
		t.Error("Connection context is not cancellable")
	}

	cancel()
	select {
	case <-streamableConn.ctx.Done():
		t.Errorf("cancelling the connection context after successful connection broke the connection")
	default:
	}
}

func TestStreamableSessionTimeout(t *testing.T) {
	// TODO: this test relies on timing and may be flaky.
	// Fixing with testing/synctest is challenging because it uses real I/O (via
	// httptest.NewServer).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := NewServer(testImpl, nil)

	deleted := make(chan string, 1)
	handler := NewStreamableHTTPHandler(
		func(req *http.Request) *Server { return server },
		&StreamableHTTPOptions{
			SessionTimeout: 50 * time.Millisecond,
		},
	)
	handler.onTransportDeletion = func(sessionID string) {
		deleted <- sessionID
	}

	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	// Connect a client to create a session.
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()

	sessionID := session.ID()
	if sessionID == "" {
		t.Fatal("client session has empty ID")
	}

	// Verify the session exists on the server.
	serverSessions := slices.Collect(server.Sessions())
	if len(serverSessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(serverSessions))
	}
	if got := serverSessions[0].ID(); got != sessionID {
		t.Fatalf("server session is %q, want %q", got, sessionID)
	}

	// Test that (possibly concurrent) requests keep the session alive.
	//
	// Spin up two goroutines, each making a request every 10ms. These requests
	// should keep the server from timing out.
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()

			for range 20 {
				if _, err := session.ListTools(ctx, nil); err != nil {
					t.Errorf("ListTools failed: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Wait for the session to be cleaned up.
	select {
	case deletedID := <-deleted:
		if deletedID != sessionID {
			t.Errorf("deleted session ID = %q, want %q", deletedID, sessionID)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for session cleanup")
	}

	// Verify the session is gone from both handler and server.
	handler.mu.Lock()
	if len(handler.sessions) != 0 {
		t.Errorf("handler.sessions is not empty; length %d", len(handler.sessions))
	}
	if ss := slices.Collect(server.Sessions()); len(ss) != 0 {
		t.Errorf("server.Sessions() is not empty; length %d", len(ss))
	}
	handler.mu.Unlock()
}

// mustNotPanic is a helper to enforce that test handlers do not panic (see
// issue #556).
func mustNotPanic(t *testing.T, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 1<<20)
				n := runtime.Stack(buf, false)
				fmt.Fprintf(os.Stderr, "handler panic: %v\n\n%s", r, buf[:n])
				t.Errorf("handler panicked: %v", r)
			}
		}()
		h.ServeHTTP(w, req)
	})
}

// TestPingEventFiltering verifies that the streamable client correctly filters
// out SSE "ping" events, which are used for keep-alive but should not be
// treated as JSON-RPC messages.
//
// This test addresses issue #636: the client should skip non-"message" events
// according to the SSE specification. It tests the fix in processStream where
// events with evt.Name != "" && evt.Name != "message" are skipped.
func TestPingEventFiltering(t *testing.T) {
	// This test verifies the low-level processStream filtering.
	// We create a mock response with ping and message events.

	sseData := `event: ping
data: ping

event: message
id: 1
data: {"jsonrpc":"2.0","id":1,"result":{}}

event: ping
data: keepalive

`

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(sseData))),
	}

	// Create a minimal streamableClientConn for testing
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incoming := make(chan jsonrpc.Message, 10)
	done := make(chan struct{})

	conn := &streamableClientConn{
		ctx:      ctx,
		done:     done,
		incoming: incoming,
	}

	// Create a test request
	testReq := &jsonrpc.Request{
		ID:     jsonrpc2.Int64ID(1),
		Method: "test",
	}

	// Process the stream
	go conn.processStream(ctx, "test", resp, testReq)

	// Collect messages with timeout
	var messages []jsonrpc.Message
	timeout := time.After(1 * time.Second)

collectLoop:
	for {
		select {
		case msg := <-incoming:
			messages = append(messages, msg)
			// We expect only 1 message (the response), not the ping events
			if len(messages) >= 1 {
				break collectLoop
			}
		case <-timeout:
			break collectLoop
		}
	}

	// Verify we only received the actual message, not the ping events
	if len(messages) != 1 {
		t.Errorf("got %d messages, want 1 (ping events should be filtered)", len(messages))
		for i, msg := range messages {
			t.Logf("message %d: %T", i, msg)
		}
	}

	// Verify the message is the response
	if len(messages) > 0 {
		resp, ok := messages[0].(*jsonrpc.Response)
		if !ok {
			t.Errorf("first message is %T, want *jsonrpc.Response", messages[0])
		} else if resp.ID.Raw() != int64(1) {
			t.Errorf("response ID is %v, want 1", resp.ID.Raw())
		}
	}
}

// TestScanEventsPingFiltering is a unit test for the low-level event scanning
// with ping events to verify scanEvents properly parses all event types.
func TestScanEventsPingFiltering(t *testing.T) {
	// Create SSE stream with mixed events
	sseData := `event: ping
data: ping

event: message
data: {"jsonrpc":"2.0","method":"test","params":{}}

event: ping
data: keepalive

event: message
data: {"jsonrpc":"2.0","method":"test2","params":{}}

`

	reader := strings.NewReader(sseData)
	var events []Event

	// Scan all events
	for evt, err := range scanEvents(reader) {
		if err != nil {
			if err != io.EOF {
				t.Fatalf("scanEvents error: %v", err)
			}
			break
		}
		events = append(events, evt)
	}

	// Verify we got all 4 events
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4", len(events))
	}

	// Verify event types
	expectedNames := []string{"ping", "message", "ping", "message"}
	for i, evt := range events {
		if evt.Name != expectedNames[i] {
			t.Errorf("event %d: got name %q, want %q", i, evt.Name, expectedNames[i])
		}
	}

	// Verify that we can decode the message events but would fail on ping events
	for i, evt := range events {
		switch evt.Name {
		case "message":
			_, err := jsonrpc.DecodeMessage(evt.Data)
			if err != nil {
				t.Errorf("event %d: failed to decode message event: %v", i, err)
			}
		case "ping":
			// Ping events have non-JSON data and should fail decoding
			_, err := jsonrpc.DecodeMessage(evt.Data)
			if err == nil {
				t.Errorf("event %d: ping event unexpectedly decoded as valid JSON-RPC", i)
			}
		}
	}
}
