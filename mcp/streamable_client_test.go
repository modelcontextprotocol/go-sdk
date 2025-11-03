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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type streamableRequestKey struct {
	httpMethod    string // http method
	sessionID     string // session ID header
	jsonrpcMethod string // jsonrpc method, or "" for non-requests
}

type header map[string]string

type streamableResponse struct {
	header              header // response headers
	status              int    // or http.StatusOK
	body                string // or ""
	optional            bool   // if set, request need not be sent
	wantProtocolVersion string // if "", unchecked
}

type fakeResponses map[streamableRequestKey]*streamableResponse

type fakeStreamableServer struct {
	t         *testing.T
	responses fakeResponses

	callMu sync.Mutex
	calls  map[streamableRequestKey]int
}

func (s *fakeStreamableServer) missingRequests() []streamableRequestKey {
	s.callMu.Lock()
	defer s.callMu.Unlock()

	var unused []streamableRequestKey
	for k, resp := range s.responses {
		if s.calls[k] == 0 && !resp.optional {
			unused = append(unused, k)
		}
	}
	return unused
}

func (s *fakeStreamableServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	key := streamableRequestKey{
		httpMethod: req.Method,
		sessionID:  req.Header.Get(sessionIDHeader),
	}
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
		}
	}

	s.callMu.Lock()
	if s.calls == nil {
		s.calls = make(map[streamableRequestKey]int)
	}
	s.calls[key]++
	s.callMu.Unlock()

	resp, ok := s.responses[key]
	if !ok {
		s.t.Errorf("missing response for %v", key)
		http.Error(w, "no response", http.StatusInternalServerError)
		return
	}
	for k, v := range resp.header {
		w.Header().Set(k, v)
	}
	status := resp.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)

	if v := req.Header.Get(protocolVersionHeader); v != resp.wantProtocolVersion && resp.wantProtocolVersion != "" {
		s.t.Errorf("%v: bad protocol version header: got %q, want %q", key, v, resp.wantProtocolVersion)
	}
	w.Write([]byte(resp.body))
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
			{"POST", "", methodInitialize}: {
				header: header{
					"Content-Type":  "application/json",
					sessionIDHeader: "123",
				},
				body: jsonBody(t, initResp),
			},
			{"POST", "123", notificationInitialized}: {
				status:              http.StatusAccepted,
				wantProtocolVersion: latestProtocolVersion,
			},
			{"GET", "123", ""}: {
				header: header{
					"Content-Type": "text/event-stream",
				},
				wantProtocolVersion: latestProtocolVersion,
			},
			{"DELETE", "123", ""}: {},
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
			{"POST", "", methodInitialize}: {
				header: header{
					"Content-Type":  "application/json",
					sessionIDHeader: "123",
				},
				body: jsonBody(t, initResp),
			},
			{"POST", "123", notificationInitialized}: {
				status:              http.StatusAccepted,
				wantProtocolVersion: latestProtocolVersion,
			},
			{"GET", "123", ""}: {
				status: http.StatusMethodNotAllowed,
			},
			{"POST", "123", methodListTools}: {
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
					{"POST", "", methodInitialize}: {
						header: header{
							"Content-Type":  "application/json; charset=utf-8", // should ignore the charset
							sessionIDHeader: "123",
						},
						body: jsonBody(t, initResp),
					},
					{"POST", "123", notificationInitialized}: {
						status:              http.StatusAccepted,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"GET", "123", ""}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status:              test.status,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"DELETE", "123", ""}: {optional: true},
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
					{"POST", "", methodInitialize}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body: jsonBody(t, initResp),
					},
					{"POST", "123", notificationInitialized}: {
						status:              test.initializedStatus,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"GET", "123", ""}: {
						header: header{
							"Content-Type": "text/event-stream",
						},
						status:              test.getStatus,
						wantProtocolVersion: latestProtocolVersion,
					},
					{"POST", "123", methodListTools}: {
						header: header{
							"Content-Type":  "application/json",
							sessionIDHeader: "123",
						},
						body:     jsonBody(t, resp(2, &ListToolsResult{Tools: []*Tool{}}, nil)),
						optional: true,
					},
					{"DELETE", "123", ""}: {optional: true},
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
			{"POST", "", methodInitialize}: {
				header: header{
					"Content-Type":  "text/event-stream",
					sessionIDHeader: "123",
				},
				body: "",
			},
			{"DELETE", "123", ""}: {optional: true},
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
