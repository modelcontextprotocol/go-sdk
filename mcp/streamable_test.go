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
			// 1. Create a server with a simple "greet" tool.
			server := NewServer(testImpl, nil)
			AddTool(server, &Tool{Name: "greet", Description: "say hi"}, sayHi)

			// 2. Start an httptest.Server with the StreamableHTTPHandler, wrapped in a
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

			// 3. Create a client and connect it to the server using our StreamableClientTransport.
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
			client := NewClient(testImpl, nil)
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
			// 4. The client calls the "greet" tool.
			params := &CallToolParams{
				Name:      "greet",
				Arguments: map[string]any{"name": "streamy"},
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

			// 5. Verify that the correct response is received.
			want := &CallToolResult{
				Content: []Content{
					&TextContent{Text: "hi streamy"},
				},
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("CallTool() returned unexpected content (-want +got):\n%s", diff)
			}
		})
	}
}

// TestClientReplay verifies that the client can recover from a
// mid-stream network failure and receive replayed messages. It uses a proxy
// that is killed and restarted to simulate a recoverable network outage.
func TestClientReplay(t *testing.T) {
	notifications := make(chan string)
	// 1. Configure the real MCP server.
	server := NewServer(testImpl, nil)

	// Use a channel to synchronize the server's message sending with the test's
	// proxy-killing action.
	serverReadyToKillProxy := make(chan struct{})
	serverClosed := make(chan struct{})
	server.AddTool(&Tool{Name: "multiMessageTool", InputSchema: &jsonschema.Schema{}},
		func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[map[string]any]) (*CallToolResult, error) {
			go func() {
				bgCtx := context.Background()
				// Send the first two messages immediately.
				ss.NotifyProgress(bgCtx, &ProgressNotificationParams{Message: "msg1"})
				ss.NotifyProgress(bgCtx, &ProgressNotificationParams{Message: "msg2"})

				// Signal the test that it can now kill the proxy.
				close(serverReadyToKillProxy)
				<-serverClosed

				// These messages should be queued for replay by the server after
				// the client's connection drops.
				ss.NotifyProgress(bgCtx, &ProgressNotificationParams{Message: "msg3"})
				ss.NotifyProgress(bgCtx, &ProgressNotificationParams{Message: "msg4"})
			}()
			return &CallToolResult{}, nil
		})
	realServer := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil))
	defer realServer.Close()
	realServerURL, err := url.Parse(realServer.URL)
	if err != nil {
		t.Fatalf("Failed to parse real server URL: %v", err)
	}

	// 2. Configure a proxy that sits between the client and the real server.
	proxyHandler := httputil.NewSingleHostReverseProxy(realServerURL)
	proxy := httptest.NewServer(proxyHandler)
	proxyAddr := proxy.Listener.Addr().String() // Get the address to restart it later.

	// 3. Configure the client to connect to the proxy with default options.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewClient(testImpl, &ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, cc *ClientSession, params *ProgressNotificationParams) {
			notifications <- params.Message
		},
	})
	clientSession, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: proxy.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer clientSession.Close()
	clientSession.CallTool(ctx, &CallToolParams{Name: "multiMessageTool"})

	// 4. Read and verify messages until the server signals it's ready for the proxy kill.
	receivedNotifications := readNotifications(t, ctx, notifications, 2)

	wantReceived := []string{"msg1", "msg2"}
	if diff := cmp.Diff(wantReceived, receivedNotifications); diff != "" {
		t.Errorf("Received notifications mismatch (-want +got):\n%s", diff)
	}

	select {
	case <-serverReadyToKillProxy:
		// Server has sent the first two messages and is paused.
	case <-ctx.Done():
		t.Fatalf("Context timed out before server was ready to kill proxy")
	}

	// 5. Simulate a total network failure by closing the proxy.
	t.Log("--- Killing proxy to simulate network failure ---")
	proxy.CloseClientConnections()
	proxy.Close()
	close(serverClosed)

	// 6. Simulate network recovery by restarting the proxy on the same address.
	t.Logf("--- Restarting proxy on %s ---", proxyAddr)
	listener, err := net.Listen("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("Failed to listen on proxy address: %v", err)
	}
	restartedProxy := &http.Server{Handler: proxyHandler}
	go restartedProxy.Serve(listener)
	defer restartedProxy.Close()

	// 7. Continue reading from the same connection object.
	// Its internal logic should successfully retry, reconnect to the new proxy,
	// and receive the replayed messages.
	recoveredNotifications := readNotifications(t, ctx, notifications, 2)

	// 8. Verify the correct messages were received on the recovered connection.
	wantRecovered := []string{"msg3", "msg4"}

	if diff := cmp.Diff(wantRecovered, recoveredNotifications); diff != "" {
		t.Errorf("Recovered notifications mismatch (-want +got):\n%s", diff)
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
	client := NewClient(testImpl, &ClientOptions{ToolListChangedHandler: func(ctx context.Context, cc *ClientSession, params *ToolListChangedParams) {
		notifications <- "toolListChanged"
	},
	})
	clientSession, err := client.Connect(ctx, &StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer clientSession.Close()
	server.AddTool(&Tool{Name: "testTool", InputSchema: &jsonschema.Schema{}},
		func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[map[string]any]) (*CallToolResult, error) {
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

	// A step is a single step in the tests below, consisting of a request payload
	// and expected response.
	type step struct {
		// If OnRequest is > 0, this step only executes after a request with the
		// given ID is received.
		//
		// All OnRequest steps must occur before the step that creates the request.
		//
		// To avoid tests hanging when there's a bug, it's expected that this
		// request is received in the course of a *synchronous* request to the
		// server (otherwise, we wouldn't be able to terminate the test without
		// analyzing a dependency graph).
		OnRequest int64
		// If set, Async causes the step to run asynchronously to other steps.
		// Redundant with OnRequest: all OnRequest steps are asynchronous.
		Async bool

		Method     string            // HTTP request method
		Send       []jsonrpc.Message // messages to send
		CloseAfter int               // if nonzero, close after receiving this many messages
		StatusCode int               // expected status code
		Recv       []jsonrpc.Message // expected messages to receive
	}

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
	initialize := step{
		Method:     "POST",
		Send:       []jsonrpc.Message{initReq},
		StatusCode: http.StatusOK,
		Recv:       []jsonrpc.Message{initResp},
	}
	initialized := step{
		Method:     "POST",
		Send:       []jsonrpc.Message{initializedMsg},
		StatusCode: http.StatusAccepted,
	}

	tests := []struct {
		name  string
		tool  func(*testing.T, context.Context, *ServerSession)
		steps []step
	}{
		{
			name: "basic",
			steps: []step{
				initialize,
				initialized,
				{
					Method:     "POST",
					Send:       []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusOK,
					Recv:       []jsonrpc.Message{resp(2, &CallToolResult{}, nil)},
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
			steps: []step{
				initialize,
				initialized,
				{
					Method: "POST",
					Send: []jsonrpc.Message{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []jsonrpc.Message{
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
			steps: []step{
				initialize,
				initialized,
				{
					Method:    "POST",
					OnRequest: 1,
					Send: []jsonrpc.Message{
						resp(1, &ListRootsResult{}, nil),
					},
					StatusCode: http.StatusAccepted,
				},
				{
					Method: "POST",
					Send: []jsonrpc.Message{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []jsonrpc.Message{
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
			steps: []step{
				initialize,
				initialized,
				{
					Method:    "POST",
					OnRequest: 1,
					Send: []jsonrpc.Message{
						resp(1, &ListRootsResult{}, nil),
					},
					StatusCode: http.StatusAccepted,
				},
				{
					Method:     "GET",
					Async:      true,
					StatusCode: http.StatusOK,
					CloseAfter: 2,
					Recv: []jsonrpc.Message{
						req(0, "notifications/progress", &ProgressNotificationParams{}),
						req(1, "roots/list", &ListRootsParams{}),
					},
				},
				{
					Method: "POST",
					Send: []jsonrpc.Message{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []jsonrpc.Message{
						resp(2, &CallToolResult{}, nil),
					},
				},
			},
		},
		{
			name: "errors",
			steps: []step{
				{
					Method:     "PUT",
					StatusCode: http.StatusMethodNotAllowed,
				},
				{
					Method:     "DELETE",
					StatusCode: http.StatusBadRequest,
				},
				{
					Method:     "POST",
					Send:       []jsonrpc.Message{req(1, "notamethod", nil)},
					StatusCode: http.StatusBadRequest, // notamethod is an invalid method
				},
				{
					Method:     "POST",
					Send:       []jsonrpc.Message{req(0, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusBadRequest, // tools/call must have an ID
				},
				{
					Method:     "POST",
					Send:       []jsonrpc.Message{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusOK,
					Recv: []jsonrpc.Message{resp(2, nil, &jsonrpc2.WireError{
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
			AddTool(server, &Tool{Name: "tool"}, func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[any]) (*CallToolResultFor[any], error) {
				if test.tool != nil {
					test.tool(t, ctx, ss)
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
				if step.OnRequest > 0 {
					blocks[step.OnRequest] = make(chan struct{})
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
			doStep := func(t *testing.T, step step) {
				if step.OnRequest > 0 {
					// Block the step until we've received the server->client request.
					mu.Lock()
					block := blocks[step.OnRequest]
					mu.Unlock()
					select {
					case <-block:
					case <-syncRequestsDone:
						t.Errorf("after all sync requests are complete, request still blocked on %d", step.OnRequest)
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
						if step.CloseAfter > 0 && len(got) == step.CloseAfter {
							cancel()
						}
					}
				}()

				gotSessionID, gotStatusCode, err := streamingRequest(ctx,
					httpServer.URL, sessionID.Load().(string), step.Method, step.Send, out)

				// Don't fail on cancelled requests: error (if any) is handled
				// elsewhere.
				if err != nil && ctx.Err() == nil {
					t.Fatal(err)
				}

				if gotStatusCode != step.StatusCode {
					t.Errorf("got status %d, want %d", gotStatusCode, step.StatusCode)
				}
				wg.Wait()

				transform := cmpopts.AcyclicTransformer("jsonrpcid", func(id jsonrpc.ID) any { return id.Raw() })
				if diff := cmp.Diff(step.Recv, got, transform); diff != "" {
					t.Errorf("received unexpected messages (-want +got):\n%s", diff)
				}
				sessionID.CompareAndSwap("", gotSessionID)
			}

			var wg sync.WaitGroup
			for _, step := range test.steps {
				if step.Async || step.OnRequest > 0 {
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
func streamingRequest(ctx context.Context, serverURL, sessionID, method string, in []jsonrpc.Message, out chan<- jsonrpc.Message) (string, int, error) {
	defer close(out)

	var body []byte
	if len(in) == 1 {
		data, err := jsonrpc2.EncodeMessage(in[0])
		if err != nil {
			return "", 0, fmt.Errorf("encoding message: %w", err)
		}
		body = data
	} else {
		var rawMsgs []json.RawMessage
		for _, msg := range in {
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

	req, err := http.NewRequestWithContext(ctx, method, serverURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, fmt.Errorf("creating request: %w", err)
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("Accept", "text/plain") // ensure multiple accept headers are allowed
	req.Header.Add("Accept", "application/json, text/event-stream")

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
		{0, 0},
		{0, 1},
		{1, 0},
		{1, 1},
		{1234, 5678},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%d_%d", test.sid, test.idx), func(t *testing.T) {
			eventID := formatEventID(test.sid, test.idx)
			gotSID, gotIdx, ok := parseEventID(eventID)
			if !ok {
				t.Fatalf("parseEventID(%q) failed, want ok", eventID)
			}
			if gotSID != test.sid || gotIdx != test.idx {
				t.Errorf("parseEventID(%q) = %d, %d, want %d, %d", eventID, gotSID, gotIdx, test.sid, test.idx)
			}
		})
	}

	invalid := []string{
		"",
		"_",
		"1_",
		"_1",
		"a_1",
		"1_a",
		"-1_1",
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
	sayHi := func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[hiParams]) (*CallToolResultFor[any], error) {
		return &CallToolResultFor[any]{Content: []Content{&TextContent{Text: "hi " + params.Arguments.Name}}}, nil
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
