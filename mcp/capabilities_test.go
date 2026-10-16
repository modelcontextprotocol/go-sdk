// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//lint:file-ignore SA1019 tests exercise deprecated SEP-2577 APIs (roots, sampling, logging).

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// TestServerListChangedNotifications verifies that listChanged notifications
// are correctly sent or suppressed based on capability configuration.
func TestServerListChangedNotifications(t *testing.T) {
	tool := &Tool{Name: "test-tool", InputSchema: &jsonschema.Schema{Type: "object"}}

	testCases := []struct {
		name            string
		serverOpts      *ServerOptions
		wantNotifyCount int64
	}{
		{
			name: "Default: notification sent",
			serverOpts: &ServerOptions{
				Capabilities: &ServerCapabilities{
					Tools: &ToolCapabilities{ListChanged: true},
				},
			},
			wantNotifyCount: 1,
		},
		{
			name: "ListChanged false: notification suppressed",
			serverOpts: &ServerOptions{
				Capabilities: &ServerCapabilities{
					Tools: &ToolCapabilities{ListChanged: false},
				},
			},
			wantNotifyCount: 0,
		},
		{
			name: "ListChanged true: notification sent",
			serverOpts: &ServerOptions{
				Capabilities: &ServerCapabilities{
					Tools: &ToolCapabilities{ListChanged: true},
				},
			},
			wantNotifyCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()

				// Create server.
				impl := &Implementation{Name: "testServer", Version: "v1.0.0"}
				server := NewServer(impl, tc.serverOpts)

				// Track notifications.
				var notifyCount atomic.Int64

				// Connect client and server.
				cTransport, sTransport := NewInMemoryTransports()
				ss, err := server.Connect(ctx, sTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer ss.Close()

				client := NewClient(&Implementation{Name: "testClient", Version: "v1.0.0"}, &ClientOptions{
					ToolListChangedHandler: func(ctx context.Context, req *ToolListChangedRequest) {
						notifyCount.Add(1)
					},
				})
				cs, err := client.Connect(ctx, cTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()

				// Add a tool, which may or may not trigger notification.
				server.AddTool(tool, nil)

				// Sleep an arbitrary time longer than the debounce delay (synctest
				// makes this practical).
				time.Sleep(1 * time.Second)

				// Wait for all goroutines to be blocked (notification delivered).
				synctest.Wait()

				if got, want := notifyCount.Load(), tc.wantNotifyCount; got != want {
					t.Errorf("notification count: got %d, want %d", got, want)
				}
			})
		})
	}
}

// TestClientListChangedNotifications verifies that roots listChanged notifications
// are correctly sent or suppressed based on client capability configuration.
func TestClientListChangedNotifications(t *testing.T) {
	root := &Root{URI: "file:///test"}

	testCases := []struct {
		name            string
		clientOpts      *ClientOptions
		wantNotifyCount int64
	}{
		{
			name:            "Default: notification sent",
			clientOpts:      nil,
			wantNotifyCount: 1,
		},
		{
			name: "ListChanged false: notification suppressed",
			clientOpts: &ClientOptions{
				Capabilities: &ClientCapabilities{
					RootsV2: &RootCapabilities{ListChanged: false},
				},
			},
			wantNotifyCount: 0,
		},
		{
			name: "ListChanged true: notification sent",
			clientOpts: &ClientOptions{
				Capabilities: &ClientCapabilities{
					RootsV2: &RootCapabilities{ListChanged: true},
				},
			},
			wantNotifyCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()

				// Track notifications.
				var notifyCount atomic.Int64

				// Create server with roots list changed handler.
				server := NewServer(&Implementation{Name: "testServer", Version: "v1.0.0"}, &ServerOptions{
					RootsListChangedHandler: func(ctx context.Context, req *RootsListChangedRequest) {
						notifyCount.Add(1)
					},
				})

				// Connect client and server.
				cTransport, sTransport := NewInMemoryTransports()
				ss, err := server.Connect(ctx, sTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer ss.Close()

				client := NewClient(&Implementation{Name: "testClient", Version: "v1.0.0"}, tc.clientOpts)
				cs, err := client.Connect(ctx, cTransport, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()

				// Add a root, which may or may not trigger notification.
				client.AddRoots(root)

				// Wait for all goroutines to be blocked (notification delivered).
				synctest.Wait()

				if got, want := notifyCount.Load(), tc.wantNotifyCount; got != want {
					t.Errorf("notification count: got %d, want %d", got, want)
				}
			})
		})
	}
}

var (
	calculatorTools  = []*Tool{{Name: "calculator", InputSchema: map[string]any{"type": "object"}}}
	samplingMessages = []*SamplingMessageV2{{Role: "user", Content: []Content{&TextContent{Text: "hi"}}}}
)

// A ServerSession doesn't send a request that needs a capability the client
// didn't declare. In particular, servers must not send tool-enabled sampling
// requests to a client that hasn't declared sampling.tools (2025-11-25
// client/sampling, "Tools in Sampling").
func TestServerSessionChecksClientCapabilities(t *testing.T) {
	basicSampling := func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
		return &CreateMessageResult{Model: "m", Content: &TextContent{}}, nil
	}
	toolSampling := func(context.Context, *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
		return &CreateMessageWithToolsResult{Model: "m", Content: []Content{&TextContent{}}}, nil
	}
	listRoots := func(ctx context.Context, ss *ServerSession) error {
		_, err := ss.ListRoots(ctx, nil)
		return err
	}
	createMessage := func(ctx context.Context, ss *ServerSession) error {
		_, err := ss.CreateMessage(ctx, &CreateMessageParams{MaxTokens: 1})
		return err
	}
	createMessageWithTools := func(params *CreateMessageWithToolsParams) func(context.Context, *ServerSession) error {
		return func(ctx context.Context, ss *ServerSession) error {
			_, err := ss.CreateMessageWithTools(ctx, params)
			return err
		}
	}
	withTools := &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools}
	withToolChoice := &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, ToolChoice: &ToolChoice{Mode: "none"}}

	for _, tc := range []struct {
		name    string
		opts    *ClientOptions
		call    func(context.Context, *ServerSession) error
		wantErr string // empty if the request goes out
	}{
		{name: "roots", call: listRoots},
		{name: "roots not declared", opts: &ClientOptions{Capabilities: &ClientCapabilities{}}, call: listRoots, wantErr: "does not support roots"},
		{name: "sampling", opts: &ClientOptions{CreateMessageHandler: basicSampling}, call: createMessage},
		{name: "sampling not declared", call: createMessage, wantErr: "does not support sampling"},
		{name: "sampling not declared, with tools API", call: createMessageWithTools(&CreateMessageWithToolsParams{MaxTokens: 1}), wantErr: "does not support sampling"},
		{name: "tools", opts: &ClientOptions{CreateMessageWithToolsHandler: toolSampling}, call: createMessageWithTools(withTools)},
		{name: "tools not declared", opts: &ClientOptions{CreateMessageHandler: basicSampling}, call: createMessageWithTools(withTools), wantErr: "does not support sampling with tools"},
		{name: "toolChoice not declared", opts: &ClientOptions{CreateMessageHandler: basicSampling}, call: createMessageWithTools(withToolChoice), wantErr: "does not support sampling with tools"},
		{
			name: "tools not in explicit capabilities",
			opts: &ClientOptions{
				CreateMessageWithToolsHandler: toolSampling,
				Capabilities:                  &ClientCapabilities{Sampling: &SamplingCapabilities{}},
			},
			call:    createMessageWithTools(withTools),
			wantErr: "does not support sampling with tools",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var received atomic.Int32
			client := NewClient(testImpl, tc.opts)
			client.AddReceivingMiddleware(func(next MethodHandler) MethodHandler {
				return func(ctx context.Context, method string, req Request) (Result, error) {
					if method == methodListRoots || method == methodCreateMessage {
						received.Add(1)
					}
					return next(ctx, method, req)
				}
			})
			ct, st := NewInMemoryTransports()
			ss, err := NewServer(testImpl, nil).Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := client.Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20251125})
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()

			err = tc.call(ctx, ss)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("got error %v, want none", err)
				}
				if got := received.Load(); got != 1 {
					t.Errorf("client received %d requests, want 1", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got error %v, want one containing %q", err, tc.wantErr)
			}
			if got := received.Load(); got != 0 {
				t.Errorf("client received %d requests, want none", got)
			}
		})
	}
}

// A client answers a request that uses a capability it didn't declare with
// invalid params, before its handler sees it: a sampling request with tools
// (the tools field of CreateMessageRequestParams in the 2025-11-25 schema) or
// an elicitation request for an undeclared mode (2025-11-25
// client/elicitation, "Error Handling").
func TestClientChecksDeclaredCapabilities(t *testing.T) {
	var emptyTools CreateMessageWithToolsParams
	if err := json.Unmarshal([]byte(`{"maxTokens":1,"messages":[],"tools":[]}`), &emptyTools); err != nil {
		t.Fatal(err)
	}
	formOnly := &ClientCapabilities{Elicitation: &ElicitationCapabilities{Form: &FormElicitationCapabilities{}}}
	urlOnly := &ClientCapabilities{Elicitation: &ElicitationCapabilities{URL: &URLElicitationCapabilities{}}}
	modeless := &ClientCapabilities{Elicitation: &ElicitationCapabilities{}}
	form := &ElicitParams{Message: "m", RequestedSchema: &jsonschema.Schema{Type: "object"}}
	url := &ElicitParams{Mode: "url", Message: "m", URL: "https://example.com/form", ElicitationID: "e1"}

	for _, tc := range []struct {
		name        string
		samplingAPI string // "basic", "tools" or "" for no sampling handler
		caps        *ClientCapabilities
		sampling    *CreateMessageWithToolsParams
		elicit      *ElicitParams
		wantCode    int64 // 0 if the handler is called
	}{
		{name: "tools to basic handler", samplingAPI: "basic", sampling: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools}, wantCode: jsonrpc.CodeInvalidParams},
		{name: "toolChoice to basic handler", samplingAPI: "basic", sampling: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, ToolChoice: &ToolChoice{Mode: "auto"}}, wantCode: jsonrpc.CodeInvalidParams},
		{name: "empty tools array to basic handler", samplingAPI: "basic", sampling: &emptyTools, wantCode: jsonrpc.CodeInvalidParams},
		{name: "no tools to basic handler", samplingAPI: "basic", sampling: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages}},
		{name: "tools to tools handler", samplingAPI: "tools", sampling: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools}},
		{name: "tools to tools handler without declared tools", samplingAPI: "tools", caps: &ClientCapabilities{Sampling: &SamplingCapabilities{}}, sampling: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools}, wantCode: jsonrpc.CodeInvalidParams},
		{name: "tools without a sampling handler", sampling: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools}, wantCode: codeUnsupportedMethod},
		{name: "url to inferred", elicit: url, wantCode: jsonrpc.CodeInvalidParams},
		{name: "form to inferred", elicit: form},
		{name: "url to form only", caps: formOnly, elicit: url, wantCode: jsonrpc.CodeInvalidParams},
		{name: "url to modeless", caps: modeless, elicit: url, wantCode: jsonrpc.CodeInvalidParams},
		{name: "form to modeless", caps: modeless, elicit: form},
		{name: "form to url only", caps: urlOnly, elicit: form, wantCode: jsonrpc.CodeInvalidParams},
		{name: "url to url only", caps: urlOnly, elicit: url},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			opts := &ClientOptions{Capabilities: tc.caps}
			switch tc.samplingAPI {
			case "basic":
				opts.CreateMessageHandler = func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
					called = true
					return &CreateMessageResult{Model: "m", Content: &TextContent{}}, nil
				}
			case "tools":
				opts.CreateMessageWithToolsHandler = func(context.Context, *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
					called = true
					return &CreateMessageWithToolsResult{Model: "m", Content: []Content{&TextContent{}}}, nil
				}
			}
			if tc.elicit != nil {
				opts.ElicitationHandler = func(context.Context, *ElicitRequest) (*ElicitResult, error) {
					called = true
					return &ElicitResult{Action: "decline"}, nil
				}
			}
			c := NewClient(testImpl, opts)

			var err error
			if tc.sampling != nil {
				_, err = c.createMessage(context.Background(), &CreateMessageWithToolsRequest{Params: tc.sampling})
			} else {
				_, err = c.elicit(context.Background(), &ElicitRequest{Params: tc.elicit})
			}
			if tc.wantCode == 0 {
				if err != nil || !called {
					t.Errorf("got error %v, handler called = %t; want the handler's result", err, called)
				}
				return
			}
			if code := errorCode(err); code != tc.wantCode {
				t.Errorf("got error %v (code %d), want code %d", err, code, tc.wantCode)
			}
			if called {
				t.Error("the handler was called")
			}
		})
	}
}

// Input requests of a multi-round-trip result go through the same checks:
// the client's own on protocol version 2026-07-28, and the server's where it
// fulfills them for an older client.
func TestInputRequestsChecked(t *testing.T) {
	newServer := func() *Server {
		s := NewServer(testImpl, nil)
		AddTool(s, &Tool{Name: "sample"}, func(context.Context, *CallToolRequest, struct{}) (*CallToolResult, any, error) {
			return &CallToolResult{InputRequests: InputRequestMap{
				"s": &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools},
			}}, nil, nil
		})
		return s
	}
	for _, tc := range []struct {
		name    string
		connect func(*testing.T, *Server, *ClientOptions) *ClientSession
	}{
		{"2026-07-28", mustConnect},
		{"2025-11-25", mustConnectOldProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called atomic.Bool
			cs := tc.connect(t, newServer(), &ClientOptions{
				CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
					called.Store(true)
					return &CreateMessageResult{Model: "m", Content: &TextContent{}}, nil
				},
			})
			res, err := cs.CallTool(t.Context(), &CallToolParams{Name: "sample"})
			if err == nil && !res.IsError {
				t.Fatalf("CallTool succeeded, want a failure about sampling with tools")
			}
			if err != nil && !strings.Contains(err.Error(), "does not support sampling with tools") {
				t.Errorf("CallTool error = %v, want one about sampling with tools", err)
			}
			if called.Load() {
				t.Error("the client's handler was called")
			}
		})
	}
}

func TestMultiRoundTripDeclaredCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   InputRequest
		caps    *ClientCapabilities
		wantErr bool
	}{
		{
			name:  "sampling with tools",
			input: &CreateMessageWithToolsParams{MaxTokens: 1, Messages: samplingMessages, Tools: calculatorTools},
		},
		{
			name:  "form elicitation",
			input: &ElicitParams{Message: "Continue?"},
		},
		{
			name:  "url elicitation",
			input: &ElicitParams{Mode: "url", Message: "Sign in", URL: "https://example.com/login", ElicitationID: "e1"},
			caps:  &ClientCapabilities{Elicitation: &ElicitationCapabilities{URL: &URLElicitationCapabilities{}}},
		},
		{
			name:    "url to form-only client",
			input:   &ElicitParams{Mode: "url", Message: "Sign in", URL: "https://example.com/login", ElicitationID: "e1"},
			wantErr: true,
		},
		{
			name:    "form to url-only client",
			input:   &ElicitParams{Message: "Continue?"},
			caps:    &ClientCapabilities{Elicitation: &ElicitationCapabilities{URL: &URLElicitationCapabilities{}}},
			wantErr: true,
		},
	} {
		for _, conn := range []struct {
			version string
			connect func(*testing.T, *Server, *ClientOptions) *ClientSession
		}{
			{protocolVersion20260728, mustConnect},
			{protocolVersion20251125, mustConnectOldProtocol},
		} {
			t.Run(tc.name+"/"+conn.version, func(t *testing.T) {
				var calls atomic.Int32
				s := NewServer(testImpl, nil)
				AddTool(s, &Tool{Name: "ask"}, func(_ context.Context, req *CallToolRequest, _ struct{}) (*CallToolResult, any, error) {
					if len(req.Params.InputResponses) == 0 {
						return &CallToolResult{InputRequests: InputRequestMap{"q": tc.input}}, nil, nil
					}
					return &CallToolResult{Content: []Content{&TextContent{Text: "done"}}}, nil, nil
				})
				opts := &ClientOptions{Capabilities: tc.caps}
				switch tc.input.(type) {
				case *CreateMessageWithToolsParams:
					opts.CreateMessageWithToolsHandler = func(context.Context, *CreateMessageWithToolsRequest) (*CreateMessageWithToolsResult, error) {
						calls.Add(1)
						return &CreateMessageWithToolsResult{Model: "m", Content: []Content{&TextContent{Text: "answer"}}}, nil
					}
				case *ElicitParams:
					opts.ElicitationHandler = func(context.Context, *ElicitRequest) (*ElicitResult, error) {
						calls.Add(1)
						return &ElicitResult{Action: "decline"}, nil
					}
				}
				cs := conn.connect(t, s, opts)
				res, err := cs.CallTool(t.Context(), &CallToolParams{Name: "ask"})
				if tc.wantErr {
					if err == nil || !strings.Contains(err.Error(), "does not support") {
						t.Fatalf("CallTool = %+v, %v; want an unsupported-capability error", res, err)
					}
					if got := calls.Load(); got != 0 {
						t.Errorf("handler called %d times, want 0", got)
					}
					return
				}
				if err != nil || res.IsError {
					t.Fatalf("CallTool = %+v, %v; want success", res, err)
				}
				if got := calls.Load(); got != 1 {
					t.Errorf("handler called %d times, want 1", got)
				}
			})
		}
	}
}
