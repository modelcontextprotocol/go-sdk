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
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

func TestStreamableTransports(t *testing.T) {
	// This test checks that the streamable server and client transports can
	// communicate.

	ctx := context.Background()

	// 1. Create a server with a simple "greet" tool.
	server := NewServer("testServer", "v1.0.0", nil)
	server.AddTools(NewServerTool("greet", "say hi", sayHi))

	// 2. Start an httptest.Server with the StreamableHTTPHandler, wrapped in a
	// cookie-checking middleware.
	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	transport := NewStreamableClientTransport(httpServer.URL, &StreamableClientTransportOptions{
		HTTPClient: httpClient,
	})
	client := NewClient("testClient", "v1.0.0", nil)
	session, err := client.Connect(ctx, transport)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()
	sid := session.ID()
	if sid == "" {
		t.Error("empty session ID")
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

	// 5. Verify that the correct response is received.
	want := &CallToolResult{
		Content: []Content{
			&TextContent{Text: "hi streamy"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CallTool() returned unexpected content (-want +got):\n%s", diff)
	}
}

// TestClientTransportRetriesPost simulates a server that fails a few POST requests
// before succeeding, verifying the client's retry mechanism.
func TestClientTransportRetriesPost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		postAttempt      atomic.Int32
		expectedAttempts = 3 // Fail twice, succeed on third attempt
	)

	// Mock server that fails POST requests for a few attempts.
	server := NewServer("mockServer", "v1.0.0", nil)
	server.AddTools(NewServerTool("greet", "say hi", sayHi))

	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// Read body to inspect if it's the specific tool call being tested
			bodyBytes, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				t.Errorf("Failed to read request body: %v", readErr)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body for handler.ServeHTTP

			var rawMsg json.RawMessage
			if err := json.Unmarshal(bodyBytes, &rawMsg); err == nil {
				var msg JSONRPCMessage
				if m, decodeErr := jsonrpc2.DecodeMessage(rawMsg); decodeErr == nil {
					msg = m
				}

				if reqMsg, ok := msg.(*JSONRPCRequest); ok && reqMsg.Method == "tools/call" {
					currentAttempt := postAttempt.Add(1)
					if currentAttempt <= int32(expectedAttempts-1) { // Fail for expectedAttempts-1 times
						t.Logf("Server: Failing POST attempt %d with 503 (tool call)", currentAttempt)
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					t.Logf("Server: Succeeding POST attempt %d (tool call)", currentAttempt)
					// For the successful attempt, allow the normal handler to proceed.
					// This will eventually return 200 OK after the JSON-RPC response is ready.
				}
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	transport := NewStreamableClientTransport(httpServer.URL, &StreamableClientTransportOptions{
		MaxRetries:     expectedAttempts - 1, // Allow 2 retries (total 3 attempts)
		InitialBackoff: 1 * time.Millisecond, // Small backoff for faster test
	})
	client := NewClient("testClient", "v1.0.0", nil)
	session, err := client.Connect(ctx, transport)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()

	params := &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "retrytest"},
	}

	callErr := make(chan error, 1)
	go func() {
		_, err = session.CallTool(ctx, params)
		callErr <- err
	}()

	select {
	case <-ctx.Done():
		t.Fatal("Test timed out before client could complete retries")
	case err = <-callErr:
		if err != nil {
			t.Fatalf("CallTool() failed unexpectedly: %v", err)
		}
		if postAttempt.Load() != int32(expectedAttempts) {
			t.Errorf("Expected %d POST attempts, got %d", expectedAttempts, postAttempt.Load())
		}
	}
}

// TestStreamableClientReplayEvents simulates a client reconnecting after a network
// interruption (simulated by server returning errors) and verifies that missed SSE events
// are replayed by the server upon successful reconnection.
func TestStreamableClientReplayEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		counter       atomic.Int32
		msgCount      atomic.Int32 // Counts how many messages have been received
		totalMessages = 10
	)

	// Mock server that notifies progress to the client
	server := NewServer("testServer", "v1.0.0", nil)
	server.AddTools(NewServerTool("noop", "no operation", func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[any]) (*CallToolResultFor[any], error) {
		// Send totalMessages notifications from the server
		for i := range totalMessages {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				err := ss.NotifyProgress(ctx, &ProgressNotificationParams{
					ProgressToken: "test-token",
					Message:       fmt.Sprintf("Message %d", i),
					Progress:      float64(i),
				})
				if err != nil {
					// Connection might be closed, this is expected if client disconnects or server handler returns error
					t.Logf("Server failed to send message %d: %v", i, err)
					return nil, err
				}
				time.Sleep(10 * time.Millisecond) // Small delay between messages
			}
		}
		return &CallToolResultFor[any]{}, nil
	}))

	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Server received %s %s with headers: Mcp-Session-Id=%q, Last-Event-ID=%q",
			r.Method, r.URL.Path, r.Header.Get("Mcp-Session-Id"), r.Header.Get("Last-Event-ID"))

		bodyBytes, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("Failed to read request body: %v", readErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body for handler.ServeHTTP

		var rawMsg json.RawMessage
		if err := json.Unmarshal(bodyBytes, &rawMsg); err == nil {
			var msg JSONRPCMessage
			if m, decodeErr := jsonrpc2.DecodeMessage(rawMsg); decodeErr == nil {
				msg = m
			}
			if reqMsg, ok := msg.(*JSONRPCRequest); ok && reqMsg.Method == "tools/call" {
				count := counter.Load()
				// simulate alternating failures
				if count%2 == 0 {
					counter.Add(1)
					t.Logf("Server: Simulating connection failure (attempt %d) with 503 Service Unavailable", count)
					w.WriteHeader(http.StatusServiceUnavailable) // Retryable error
					return
				}
			}
		}

		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	transport := NewStreamableClientTransport(httpServer.URL, &StreamableClientTransportOptions{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond, // Small backoff for faster test
	})
	client := NewClient("testClient", "v1.0.0", nil)
	session, err := client.Connect(ctx, transport)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	defer session.Close()

	// Collect messages received by the client
	receivedMessages := make(chan string, totalMessages)
	client.opts.ProgressNotificationHandler = func(ctx context.Context, cs *ClientSession, params *ProgressNotificationParams) {
		receivedMessages <- params.Message
		msgCount.Add(1)
	}

	// Trigger messages from the server by calling a noop tool.
	// This will happen concurrently with the client's GET retries.
	go func() {
		_, callErr := session.CallTool(ctx, &CallToolParams{Name: "noop"})
		if callErr != nil && !strings.Contains(callErr.Error(), "context canceled") {
			t.Errorf("CallTool returned unexpected error: %v", callErr)
		}
	}()

	// Wait for all messages to be received, or timeout
	allMessages := []string{}
	for len(allMessages) < totalMessages {
		select {
		case <-ctx.Done():
			t.Fatalf("Test timed out. Received %d messages, expected %d. Last messages: %v", len(allMessages), totalMessages, allMessages)
		case msg := <-receivedMessages:
			allMessages = append(allMessages, msg)
		}
	}

	// Verify all messages were received in order
	expectedMessages := make([]string, totalMessages)
	for i := range totalMessages {
		expectedMessages[i] = fmt.Sprintf("Message %d", i)
	}

	if diff := cmp.Diff(expectedMessages, allMessages); diff != "" {
		t.Errorf("Received messages mismatch (-want +got):\n%s", diff)
	}
}

// TestStreamableClientSessionTermination verifies that the client correctly
// sends a DELETE request to terminate the session when Close() is called.
func TestStreamableClientSessionTermination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var establishedSessionID atomic.Value // Stores the session ID we expect to see deleted
	establishedSessionID.Store("")
	deleteReceived := sync.WaitGroup{}
	deleteReceived.Add(1)
	// Server that records session IDs and responds to DELETE
	server := NewServer("testServer", "v1.0.0", nil)
	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Server received %s %s with headers: Mcp-Session-Id=%q, Last-Event-ID=%q",
			r.Method, r.URL.Path, r.Header.Get("Mcp-Session-Id"), r.Header.Get("Last-Event-ID"))
		if r.Method == http.MethodDelete {
			if id := r.Header.Get("Mcp-Session-Id"); id != "" {
				establishedSessionID.Store(id)
				deleteReceived.Done()
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	transport := NewStreamableClientTransport(httpServer.URL, nil)
	client := NewClient("testClient", "v1.0.0", nil)
	session, err := client.Connect(ctx, transport)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}

	// Make a dummy call to ensure sessionID is established on the client side
	// This also ensures the server handler sets the ID, which is picked up by the test hook.
	session.CallTool(ctx, &CallToolParams{Name: "dummy", Arguments: map[string]any{}})

	// Close the session
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close() failed: %v", err)
	}
	deleteReceived.Wait()
}

// TestStreamableServerDeleteWithoutSessionID verifies that a DELETE request
// without an Mcp-Session-Id header returns a 400 Bad Request.
func TestStreamableServerDeleteWithoutSessionID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := NewServer("testServer", "v1.0.0", nil)
	handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
	defer handler.closeAll()

	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	// Make a DELETE request without setting the Mcp-Session-Id header
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, httpServer.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create DELETE request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status %d (Bad Request) for DELETE without session ID, got %d", http.StatusBadRequest, resp.StatusCode)
	} else {
		t.Logf("Received expected status %d for DELETE without session ID.", resp.StatusCode)
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

		Method     string           // HTTP request method
		Send       []JSONRPCMessage // messages to send
		CloseAfter int              // if nonzero, close after receiving this many messages
		StatusCode int              // expected status code
		Recv       []JSONRPCMessage // expected messages to receive
	}

	// JSON-RPC message constructors.
	req := func(id int64, method string, params any) *JSONRPCRequest {
		r := &JSONRPCRequest{
			Method: method,
			Params: mustMarshal(t, params),
		}
		if id > 0 {
			r.ID = jsonrpc2.Int64ID(id)
		}
		return r
	}
	resp := func(id int64, result any, err error) *JSONRPCResponse {
		return &JSONRPCResponse{
			ID:     jsonrpc2.Int64ID(id),
			Result: mustMarshal(t, result),
			Error:  err,
		}
	}

	// Predefined steps, to avoid repetition below.
	initReq := req(1, "initialize", &InitializeParams{})
	initResp := resp(1, &InitializeResult{
		Capabilities: &serverCapabilities{
			Completions: &completionCapabilities{},
			Logging:     &loggingCapabilities{},
			Prompts:     &promptCapabilities{ListChanged: true},
			Resources:   &resourceCapabilities{ListChanged: true},
			Tools:       &toolCapabilities{ListChanged: true},
		},
		ProtocolVersion: "2025-03-26",
		ServerInfo:      &implementation{Name: "testServer", Version: "v1.0.0"},
	}, nil)
	initializedMsg := req(0, "initialized", &InitializedParams{})
	initialize := step{
		Method:     "POST",
		Send:       []JSONRPCMessage{initReq},
		StatusCode: http.StatusOK,
		Recv:       []JSONRPCMessage{initResp},
	}
	initialized := step{
		Method:     "POST",
		Send:       []JSONRPCMessage{initializedMsg},
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
					Send:       []JSONRPCMessage{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusOK,
					Recv:       []JSONRPCMessage{resp(2, &CallToolResult{}, nil)},
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
					Send: []JSONRPCMessage{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{
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
					Send: []JSONRPCMessage{
						resp(1, &ListRootsResult{}, nil),
					},
					StatusCode: http.StatusAccepted,
				},
				{
					Method: "POST",
					Send: []JSONRPCMessage{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{
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
					Send: []JSONRPCMessage{
						resp(1, &ListRootsResult{}, nil),
					},
					StatusCode: http.StatusAccepted,
				},
				{
					Method:     "GET",
					Async:      true,
					StatusCode: http.StatusOK,
					CloseAfter: 2,
					Recv: []JSONRPCMessage{
						req(0, "notifications/progress", &ProgressNotificationParams{}),
						req(1, "roots/list", &ListRootsParams{}),
					},
				},
				{
					Method: "POST",
					Send: []JSONRPCMessage{
						req(2, "tools/call", &CallToolParams{Name: "tool"}),
					},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{
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
					Send:       []JSONRPCMessage{req(2, "tools/call", &CallToolParams{Name: "tool"})},
					StatusCode: http.StatusOK,
					Recv: []JSONRPCMessage{resp(2, nil, &jsonrpc2.WireError{
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
			server := NewServer("testServer", "v1.0.0", nil)
			tool := NewServerTool("tool", "test tool", func(ctx context.Context, ss *ServerSession, params *CallToolParamsFor[any]) (*CallToolResultFor[any], error) {
				if test.tool != nil {
					test.tool(t, ctx, ss)
				}
				return &CallToolResultFor[any]{}, nil
			})
			server.AddTools(tool)

			// Start the streamable handler.
			handler := NewStreamableHTTPHandler(func(req *http.Request) *Server { return server }, nil)
			defer handler.closeAll()

			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			// blocks records request blocks by JSONRPC ID.
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
				var got []JSONRPCMessage
				out := make(chan JSONRPCMessage)
				// Cancel the step if we encounter a request that isn't going to be
				// handled.
				ctx, cancel := context.WithCancel(context.Background())

				var wg sync.WaitGroup
				wg.Add(1)
				go func() {
					defer wg.Done()

					for m := range out {
						if req, ok := m.(*JSONRPCRequest); ok && req.ID.IsValid() {
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

				transform := cmpopts.AcyclicTransformer("jsonrpcid", func(id JSONRPCID) any { return id.Raw() })
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
func streamingRequest(ctx context.Context, serverURL, sessionID, method string, in []JSONRPCMessage, out chan<- JSONRPCMessage) (string, int, error) {
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
			msg, err := jsonrpc2.DecodeMessage(evt.data)
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

func TestEventID(t *testing.T) {
	tests := []struct {
		sid streamID
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
