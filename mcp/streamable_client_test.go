// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type streamableRequestKey struct {
	httpMethod    string // http method
	sessionID     string // session ID header
	jsonrpcMethod string // jsonrpc method, or "" for non-requests
	lastEventID   string // Last-Event-ID header
}

type header map[string]string

// TODO: replace body and status fields with responseFunc; add helpers to reduce duplication.
type streamableResponse struct {
	header              header                                 // response headers
	status              int                                    // or http.StatusOK; ignored if responseFunc is set
	body                string                                 // or ""; ignored if responseFunc is set
	responseFunc        func(r *jsonrpc.Request) (string, int) // if set, overrides body and status
	optional            bool                                   // if set, request need not be sent
	wantProtocolVersion string                                 // if "", unchecked
	done                chan struct{}                          // if set, receive from this channel before terminating the request
}

type fakeResponses map[streamableRequestKey]*streamableResponse

type fakeStreamableServer struct {
	t         *testing.T
	responses fakeResponses

	calledMu sync.Mutex
	called   map[streamableRequestKey]bool
}

func (s *fakeStreamableServer) missingRequests() []streamableRequestKey {
	s.calledMu.Lock()
	defer s.calledMu.Unlock()

	var unused []streamableRequestKey
	for k, resp := range s.responses {
		if !s.called[k] && !resp.optional {
			unused = append(unused, k)
		}
	}
	return unused
}

func (s *fakeStreamableServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	key := streamableRequestKey{
		httpMethod:  req.Method,
		sessionID:   req.Header.Get(sessionIDHeader),
		lastEventID: req.Header.Get("Last-Event-ID"), // TODO: extract this to a constant, like sessionIDHeader
	}
	var jsonrpcReq *jsonrpc.Request
	if req.Method == http.MethodPost {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			s.t.Errorf("failed to read body: %v", err)
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}
		msg, err := jsonrpc.DecodeMessage(body)
		if err != nil {
			s.t.Errorf("invalid body: %v", err)
			http.Error(w, "invalid body", http.StatusInternalServerError)
			return
		}
		if r, ok := msg.(*jsonrpc.Request); ok {
			key.jsonrpcMethod = r.Method
			jsonrpcReq = r
		}
	}

	s.calledMu.Lock()
	if s.called == nil {
		s.called = make(map[streamableRequestKey]bool)
	}
	s.called[key] = true
	s.calledMu.Unlock()

	resp, ok := s.responses[key]
	if !ok {
		s.t.Errorf("missing response for %v", key)
		http.Error(w, "no response", http.StatusInternalServerError)
		return
	}

	// Determine body and status, potentially using responseFunc for dynamic responses.
	body := resp.body
	status := resp.status
	if resp.responseFunc != nil {
		body, status = resp.responseFunc(jsonrpcReq)
	}
	if status == 0 {
		status = http.StatusOK
	}

	for k, v := range resp.header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	w.(http.Flusher).Flush() // flush response headers

	if v := req.Header.Get(protocolVersionHeader); v != resp.wantProtocolVersion && resp.wantProtocolVersion != "" {
		s.t.Errorf("%v: bad protocol version header: got %q, want %q", key, v, resp.wantProtocolVersion)
	}
	w.Write([]byte(body))
	w.(http.Flusher).Flush() // flush response

	if resp.done != nil {
		<-resp.done
	}
}

var (
	initResult = &InitializeResult{
		Capabilities: &ServerCapabilities{
			Completions: &CompletionCapabilities{},
			Logging:     &LoggingCapabilities{},
			Tools:       &ToolCapabilities{ListChanged: true},
		},
		ProtocolVersion: latestProtocolVersion,
		ServerInfo:      &Implementation{Name: "testServer", Version: "v1.0.0"},
	}
	initResp = resp(1, initResult, nil)
)

func jsonBody(t *testing.T, msg jsonrpc2.Message) string {
	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		t.Fatalf("encoding failed: %v", err)
	}
	return string(data)
}

func TestStreamableClientTransportLifecycle(t *testing.T) {
	ctx := context.Background()

	// The lifecycle test verifies various behavior of the streamable client
	// initialization:
	//  - check that it can handle application/json responses
	//  - check that it sends the negotiated protocol version
	fake := &fakeStreamableServer{
		t: t,
		responses: fakeResponses{
			{"POST", "", methodInitialize, ""}: {
				header: header{
					"Content-Type":  "application/json",
					sessionIDHeader: "123",
				},
				body: jsonBody(t, initResp),
			},
			{"POST", "123", notificationInitialized, ""}: {
				status:              http.StatusAccepted,
				wantProtocolVersion: latestProtocolVersion,
			},
			{"GET", "123", "", ""}: {
				header: header{
					"Content-Type": "text/event-stream",
				},
				wantProtocolVersion: latestProtocolVersion,
			},
			{"DELETE", "123", "", ""}: {},
		},
	}

	httpServer := httptest.NewServer(fake)
	defer httpServer.Close()

	transport := &StreamableClientTransport{Endpoint: httpServer.URL}
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Errorf("closing session: %v", err)
	}
	if missing := fake.missingRequests(); len(missing) > 0 {
		t.Errorf("did not receive expected requests: %v", missing)
	}
	if diff := cmp.Diff(initResult, session.state.InitializeResult); diff != "" {
		t.Errorf("mismatch (-want, +got):\n%s", diff)
	}
}

func TestStreamableClientRedundantDelete(t *testing.T) {
	ctx := context.Background()

	// The lifecycle test verifies various behavior of the streamable client
	// initialization:
	//  - check that it can handle application/json responses
	//  - check that it sends the negotiated protocol version
	fake := &fakeStreamableServer{
		t: t,
		responses: fakeResponses{
			{"POST", "", methodInitialize, ""}: {
				header: header{
					"Content-Type":  "application/json",
					sessionIDHeader: "123",
				},
				body: jsonBody(t, initResp),
			},
			{"POST", "123", notificationInitialized, ""}: {
				status:              http.StatusAccepted,
				wantProtocolVersion: latestProtocolVersion,
			},
			{"GET", "123", "", ""}: {
				status: http.StatusMethodNotAllowed,
			},
			{"POST", "123", methodListTools, ""}: {
				status: http.StatusNotFound,
			},
		},
	}

	httpServer := httptest.NewServer(fake)
	defer httpServer.Close()

	transport := &StreamableClientTransport{Endpoint: httpServer.URL}
	client := NewClient(testImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect() failed: %v", err)
	}
	_, err = session.ListTools(ctx, nil)
	if err == nil {
		t.Errorf("Listing tools: got nil error, want non-nil")
	}
	_ = session.Wait() // must not hang
	if missing := fake.missingRequests(); len(missing) > 0 {
		t.Errorf("did not receive expected requests: %v", missing)
	}
}

func TestStreamableClientGETHandling(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		status              int
		wantErrorContaining string
	}{
		{http.StatusOK, ""},
		{http.StatusMethodNotAllowed, ""},
		// The client error status code is not treated as an error in non-strict
		// mode.
		{http.StatusNotFound, ""},
		{http.StatusBadRequest, ""},
		{http.StatusInternalServerError, "standalone SSE"},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("status=%d", test.status), func(t *testing.T) {
			fake := &fakeStreamableServer{
				t: t,
				responses: fakeResponses{
					{"POST", "", methodInitialize, ""}: {
						header: header{
							"Content-Type":  "application/json; charset=utf-8", // should ignore the charset
							sessionIDHeader: "123",
						},
						body: jsonBody(t, initResp),
					},
					{"POST", "123", notificationInitialized, ""}: {
						status:              http.StatusAccepted,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"GET", "123", "", ""}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status:              test.status,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"DELETE", "123", "", ""}: {optional: true},
				},
			}
			httpServer := httptest.NewServer(fake)
			defer httpServer.Close()

			transport := &StreamableClientTransport{Endpoint: httpServer.URL}
			client := NewClient(testImpl, nil)
			session, err := client.Connect(ctx, transport, nil)
			if err == nil {
				defer session.Close()
			}
			if test.wantErrorContaining != "" {
				if err == nil {
					t.Fatalf("Connect succeeded unexpectedly, want error containing %q", test.wantErrorContaining)
				}
				if got := err.Error(); !strings.Contains(got, test.wantErrorContaining) {
					t.Errorf("Connect error = %q, want containing %q", got, test.wantErrorContaining)
				}
			} else if err != nil {
				t.Fatalf("Connect failed: %v", err)
			}
		})
	}
}

func TestStreamableClientStrictness(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		label             string
		strict            bool
		initializedStatus int
		getStatus         int
		wantConnectError  bool
	}{
		{"conformant server", true, http.StatusAccepted, http.StatusMethodNotAllowed, false},
		{"strict initialized", true, http.StatusOK, http.StatusMethodNotAllowed, true},
		{"unstrict initialized", false, http.StatusOK, http.StatusMethodNotAllowed, false},
		{"strict GET", true, http.StatusAccepted, http.StatusNotFound, true},
		// The client error status code is not treated as an error in non-strict
		// mode.
		{"unstrict GET on StatusNotFound", false, http.StatusOK, http.StatusNotFound, false},
		{"unstrict GET on StatusBadRequest", false, http.StatusOK, http.StatusBadRequest, false},
		{"GET on InternlServerError", false, http.StatusOK, http.StatusInternalServerError, true},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			fake := &fakeStreamableServer{
				t: t,
				responses: fakeResponses{
					{"POST", "", methodInitialize, ""}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body: jsonBody(t, initResp),
					},
					{"POST", "123", notificationInitialized, ""}: {
						status:              test.initializedStatus,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"GET", "123", "", ""}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status:              test.getStatus,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"POST", "123", methodListTools, ""}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body:     jsonBody(t, resp(2, &ListToolsResult{Tools: []*Tool{}}, nil)),
						optional: true,
					},
					{"DELETE", "123", "", ""}: {optional: true},
				},
			}
			httpServer := httptest.NewServer(fake)
			defer httpServer.Close()

			transport := &StreamableClientTransport{Endpoint: httpServer.URL, strict: test.strict}
			client := NewClient(testImpl, nil)
			session, err := client.Connect(ctx, transport, nil)
			if (err != nil) != test.wantConnectError {
				t.Errorf("client.Connect() returned error %v; want error: %t", err, test.wantConnectError)
			}
			if err != nil {
				return
			}
			_, err = session.ListTools(ctx, nil)
			if err != nil {
				t.Errorf("ListTools failed: %v", err)
			}
			if err := session.Close(); err != nil {
				t.Errorf("closing session: %v", err)
			}
		})
	}
}

func TestStreamableClientUnresumableRequest(t *testing.T) {
	// This test verifies that the client fails fast when making a request that
	// is unresumable, because it does not contain any events.
	ctx := context.Background()
	fake := &fakeStreamableServer{
		t: t,
		responses: fakeResponses{
			{"POST", "", methodInitialize, ""}: {
				header: header{
					"Content-Type":  "text/event-stream",
					sessionIDHeader: "123",
				},
				body: "",
			},
			{"DELETE", "123", "", ""}: {optional: true},
		},
	}
	httpServer := httptest.NewServer(fake)
	defer httpServer.Close()

	transport := &StreamableClientTransport{Endpoint: httpServer.URL}
	client := NewClient(testImpl, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err == nil {
		cs.Close()
		t.Fatalf("Connect succeeded unexpectedly")
	}
	// This may be a bit of a change detector, but for now check that we're
	// actually exercising the early failure codepath.
	msg := "terminated without response"
	if !strings.Contains(err.Error(), msg) {
		t.Errorf("Connect: got error %v, want containing %q", err, msg)
	}
}

func TestStreamableClientResumption_Cancelled(t *testing.T) {
	// This test verifies that the resumed requests are closed when their context
	// is cancelled (issue #662).

	// This test (unfortunately) relies on timing, so may have false positives.
	//
	// Set the reconnect initial delay to some small(ish) value so that the test
	// doesn't take too long. But this value must be large enough that we mostly
	// avoid races in the tests below, where one test cases is intended to be in
	// between the initial attempt and first reconnection.
	//
	// For easier tuning (and debugging), factor out the tick size.
	//
	// TODO(#680): experiment with instead using synctest.
	const tick = 10 * time.Millisecond
	defer func(delay time.Duration) {
		reconnectInitialDelay = delay
	}(reconnectInitialDelay)
	reconnectInitialDelay = 2 * tick

	// The setup: terminate a request stream and make the resumed request hang
	// indefinitely. CallTool should still exit when its context is canceled.
	//
	// This should work whether we're handling the initial request, waiting to
	// retry, or handling the retry.
	//
	// Furthermore, closing the client connection should not hang, because there
	// should be no ongoing requests.

	tests := []struct {
		label       string
		cancelAfter time.Duration
	}{
		{"in process", 1 * tick}, // cancel while the request is being handled
		// initial request terminates at 2 ticks (see below)
		{"awaiting retry", 3 * tick}, // cancel in-between first and second attempt
		// retry starts at 4 ticks (=2+2)
		{"in retry", 5 * tick}, // cancel while second attempt is hanging
	}

	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			ctx := context.Background()

			// done will be closed when the test exits: used to simulate requests that
			// hang indefinitely.
			initialRequestDone := make(chan struct{}) // closed below
			allDone := make(chan struct{})

			fake := &fakeStreamableServer{
				t: t,
				responses: fakeResponses{
					{"POST", "", methodInitialize, ""}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body: jsonBody(t, initResp),
					},
					{"POST", "123", notificationInitialized, ""}: {
						status:              http.StatusAccepted,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"GET", "123", "", ""}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status: http.StatusMethodNotAllowed, // don't allow the standalone stream
					},
					{"POST", "123", methodCallTool, ""}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status: http.StatusOK,
						body: `id: 1
data: { "jsonrpc": "2.0", "method": "notifications/message", "params": { "level": "error", "data": "bad" } }

`,
						done: initialRequestDone,
					},
					{"POST", "123", methodListTools, ""}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body: jsonBody(t, resp(3, &ListToolsResult{Tools: []*Tool{}}, nil)),
					},
					{"GET", "123", "", "1"}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status: http.StatusOK,
						done:   allDone, // hang indefinitely
					},
					{"POST", "123", notificationCancelled, ""}: {status: http.StatusAccepted},
					{"DELETE", "123", "", ""}:                  {optional: true},
				},
			}
			httpServer := httptest.NewServer(fake)
			defer httpServer.Close()
			defer close(allDone) // must be deferred *after* httpServer.Close, to avoid deadlock

			transport := &StreamableClientTransport{Endpoint: httpServer.URL}
			client := NewClient(testImpl, nil)
			cs, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close() // ensure the session is closed, though we're also closing below

			// start the timer on the initial request
			go func() {
				<-time.After(2 * tick)
				close(initialRequestDone)
			}()

			// start the timer on the call cancellation
			timeoutCtx, cancel := context.WithTimeout(ctx, test.cancelAfter)
			defer cancel()

			go func() {
				<-timeoutCtx.Done()
			}()

			if _, err := cs.CallTool(timeoutCtx, &CallToolParams{
				Name: "tool",
			}); err == nil {
				t.Errorf("CallTool succeeded unexpectedly")
			}

			// ...but cancellation should not break the session.
			// Check that an arbitrary request succeeds.
			if _, err := cs.ListTools(ctx, nil); err != nil {
				t.Errorf("ListTools failed after cancellation")
			}
		})
	}
}

// TestStreamableClientTransientErrors verifies that transient errors (timeouts,
// 5xx HTTP status codes) do not permanently break the client connection.
// This tests the fix for issue #683.
func TestStreamableClientTransientErrors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		transientStatus   int    // HTTP status to return for the transient call
		wantCallError     bool   // whether the transient call should error
		wantSessionBroken bool   // whether the session should be broken after
		wantErrorContains string // substring expected in error message
	}{
		{
			transientStatus:   http.StatusServiceUnavailable,
			wantCallError:     true,
			wantSessionBroken: false,
			wantErrorContains: "Service Unavailable",
		},
		{
			transientStatus:   http.StatusBadGateway,
			wantCallError:     true,
			wantSessionBroken: false,
			wantErrorContains: "Bad Gateway",
		},
		{
			transientStatus:   http.StatusGatewayTimeout,
			wantCallError:     true,
			wantSessionBroken: false,
			wantErrorContains: "Gateway Timeout",
		},
		{
			transientStatus:   http.StatusTooManyRequests,
			wantCallError:     true,
			wantSessionBroken: false,
			wantErrorContains: "Too Many Requests",
		},
		{
			transientStatus:   http.StatusUnauthorized,
			wantCallError:     true,
			wantSessionBroken: true,
			wantErrorContains: "Unauthorized",
		},
		{
			transientStatus:   http.StatusNotFound,
			wantCallError:     true,
			wantSessionBroken: true,
			wantErrorContains: "not found", // NotFound has special handling
		},
	}

	for _, test := range tests {
		t.Run(http.StatusText(test.transientStatus), func(t *testing.T) {
			var returnedError atomic.Bool
			fake := &fakeStreamableServer{
				t: t,
				responses: fakeResponses{
					{"POST", "", methodInitialize, ""}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body: jsonBody(t, initResp),
					},
					{"POST", "123", notificationInitialized, ""}: {
						status:              http.StatusAccepted,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"GET", "123", "", ""}: {
						status: http.StatusMethodNotAllowed,
					},
					{"POST", "123", methodListTools, ""}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						responseFunc: func(r *jsonrpc.Request) (string, int) {
							// First call returns transient error, subsequent calls succeed.
							if !returnedError.Swap(true) && test.transientStatus != 0 {
								return "", test.transientStatus
							}
							return jsonBody(t, resp(r.ID.Raw().(int64), &ListToolsResult{Tools: []*Tool{}}, nil)), 0
						},
						optional: true,
					},
					{"DELETE", "123", "", ""}: {optional: true},
				},
			}

			httpServer := httptest.NewServer(fake)
			defer httpServer.Close()

			transport := &StreamableClientTransport{Endpoint: httpServer.URL}
			client := NewClient(testImpl, nil)
			session, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatalf("Connect failed: %v", err)
			}
			defer session.Close()

			// First call: should trigger transient error.
			_, err = session.ListTools(ctx, nil)
			if test.wantCallError {
				if err == nil {
					t.Error("ListTools succeeded unexpectedly, want error")
				} else if test.wantErrorContains != "" && !strings.Contains(err.Error(), test.wantErrorContains) {
					t.Errorf("ListTools error = %q, want containing %q", err.Error(), test.wantErrorContains)
				}
			} else if err != nil {
				t.Errorf("ListTools failed unexpectedly: %v", err)
			}

			// Second call: verifies whether the session is still usable.
			_, err = session.ListTools(ctx, nil)
			if test.wantSessionBroken {
				if err == nil {
					t.Error("second ListTools succeeded unexpectedly, want session broken")
				}
			} else {
				if err != nil {
					t.Errorf("second ListTools failed unexpectedly: %v (session should survive transient errors)", err)
				}
			}
		})
	}
}
