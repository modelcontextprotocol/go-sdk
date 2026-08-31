// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

//lint:file-ignore SA1019 tests exercise deprecated SEP-2577 APIs (roots, sampling, logging).

package mcp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestMultiRoundTrip_ManualRetry(t *testing.T) {
	type deployResult struct {
		Deployed bool   `json:"deployed"`
		Reason   string `json:"reason,omitempty"`
	}

	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	AddTool(srv, &Tool{Name: "deploy"}, func(ctx context.Context, req *CallToolRequest, input struct{}) (*CallToolResult, *deployResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &CallToolResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Deploy to production?"}},
				RequestState:  "deployment-123",
			}, nil, nil
		}

		resp, ok := req.Params.InputResponses["confirm"]
		if !ok {
			return &CallToolResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Please confirm (retry)"}},
			}, nil, nil
		}

		if req.Params.RequestState == "" {
			return &CallToolResult{}, &deployResult{Deployed: false, Reason: "no_state"}, nil
		}
		if elicitResult := resp.(*ElicitResult); elicitResult != nil && elicitResult.Action != "accept" {
			return &CallToolResult{}, &deployResult{Deployed: false, Reason: "cancelled"}, nil
		}

		return &CallToolResult{}, &deployResult{Deployed: true}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		MultiRoundTrip: &MultiRoundTripOptions{Disabled: true},
	})

	// Round 1: initiate deployment
	res, err := conn.CallTool(ctx, &CallToolParams{Name: "deploy"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if !res.NeedsInput() {
		t.Fatal("NeedsInput() = false, want true")
	}
	if got := len(res.InputRequests); got != 1 {
		t.Fatalf("len(res.InputRequests) = %d, want 1", got)
	}
	if _, ok := res.InputRequests["confirm"].(*ElicitParams); !ok {
		t.Fatalf("res.InputRequests[confirm] type = %T, want *ElicitParams", res.InputRequests["confirm"])
	}

	// Round 2: retry with confirmation
	res, err = conn.CallTool(ctx, &CallToolParams{
		Name: "deploy",
		InputResponses: InputResponseMap{
			"confirm": &ElicitResult{Action: "accept", Content: map[string]any{"ok": true}},
		},
		RequestState: res.RequestState,
	})
	if err != nil {
		t.Fatalf("CallTool() follow-up error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after follow-up, want false")
	}

	if diff := cmp.Diff(map[string]any{"deployed": true}, res.StructuredContent, ctrCmpOpts...); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestMultiRoundTrip_AutoRetry(t *testing.T) {

	tests := []struct {
		name          string
		inputRequests InputRequestMap
		wantResult    map[string]any
	}{
		{
			name: "elicit",
			inputRequests: InputRequestMap{
				"confirm": &ElicitParams{Message: "Deploy?"},
			},
			wantResult: map[string]any{"ids": []any{"confirm"}},
		},
		{
			name: "createMessage",
			inputRequests: InputRequestMap{
				"summarize": &CreateMessageParams{
					Messages:  []*SamplingMessage{{Role: "user", Content: &TextContent{Text: "summarize"}}},
					MaxTokens: 100,
				},
			},
			wantResult: map[string]any{"ids": []any{"summarize"}},
		},
		{
			name: "listRoots",
			inputRequests: InputRequestMap{
				"roots": &ListRootsParams{},
			},
			wantResult: map[string]any{"ids": []any{"roots"}},
		},
		{
			name: "all three",
			inputRequests: InputRequestMap{
				"confirm": &ElicitParams{Message: "OK?"},
				"draft": &CreateMessageParams{
					Messages:  []*SamplingMessage{{Role: "user", Content: &TextContent{Text: "write"}}},
					MaxTokens: 50,
				},
				"roots": &ListRootsParams{},
			},
			wantResult: map[string]any{"ids": []any{"confirm", "draft", "roots"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			srv := NewServer(testImpl, nil)
			inputRequests := tt.inputRequests
			AddTool(srv, &Tool{Name: "act"}, func(ctx context.Context, req *CallToolRequest, input struct{}) (*CallToolResult, any, error) {
				if len(req.Params.InputResponses) == 0 {
					return &CallToolResult{
						InputRequests: inputRequests,
						RequestState:  "state-1",
					}, nil, nil
				}
				// Collect the IDs of fulfilled responses.
				var ids []string
				for id := range req.Params.InputResponses {
					ids = append(ids, id)
				}
				slices.Sort(ids)
				return &CallToolResult{}, map[string]any{"ids": ids}, nil
			})

			conn := mustConnect(t, srv, &ClientOptions{
				ElicitationHandler: func(_ context.Context, req *ElicitRequest) (*ElicitResult, error) {
					return &ElicitResult{Action: "accept"}, nil
				},
				CreateMessageHandler: func(_ context.Context, req *CreateMessageRequest) (*CreateMessageResult, error) {
					return &CreateMessageResult{
						Model:   "test-model",
						Role:    "assistant",
						Content: &TextContent{Text: "response"},
					}, nil
				},
			})
			conn.client.AddRoots(&Root{URI: "file:///workspace", Name: "workspace"})

			res, err := conn.CallTool(ctx, &CallToolParams{Name: "act"})
			if err != nil {
				t.Fatalf("CallTool() error = %v", err)
			}
			if res.NeedsInput() {
				t.Fatal("NeedsInput() = true after auto-retry, want false")
			}

			// Sort the expected IDs for stable comparison.
			if wantIDs, ok := tt.wantResult["ids"].([]any); ok {
				slices.SortFunc(wantIDs, func(a, b any) int {
					if a.(string) < b.(string) {
						return -1
					}
					return 1
				})
			}

			if diff := cmp.Diff(tt.wantResult, res.StructuredContent, ctrCmpOpts...); diff != "" {
				t.Errorf("result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMultiRoundTrip_AutoRetryDoesNotMutateCallerParams verifies that
// the client middleware carries inputResponses and requestState on a
// copy of the caller's params: after CallTool returns, the caller's
// struct is unchanged, and reusing it for a second call fulfills the
// input requests again instead of silently replaying the first call's
// answers against a server that gates on them.
func TestMultiRoundTrip_AutoRetryDoesNotMutateCallerParams(t *testing.T) {
	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	AddTool(srv, &Tool{Name: "act"}, func(ctx context.Context, req *CallToolRequest, input struct{}) (*CallToolResult, any, error) {
		if len(req.Params.InputResponses) == 0 {
			return &CallToolResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Sure?"}},
				RequestState:  "state-1",
			}, nil, nil
		}
		return &CallToolResult{}, map[string]any{"ok": true}, nil
	})

	var elicitations atomic.Int32
	conn := mustConnect(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			elicitations.Add(1)
			return &ElicitResult{Action: "accept"}, nil
		},
	})

	params := &CallToolParams{Name: "act"}
	res, err := conn.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after auto-retry, want false")
	}
	if params.InputResponses != nil {
		t.Errorf("params.InputResponses = %v after CallTool, want nil (caller params must not be mutated)", params.InputResponses)
	}
	if params.RequestState != "" {
		t.Errorf("params.RequestState = %q after CallTool, want empty (caller params must not be mutated)", params.RequestState)
	}
	if got := elicitations.Load(); got != 1 {
		t.Fatalf("elicitations = %d, want 1", got)
	}

	// Reusing the same params struct must fulfill the input requests
	// again, not replay the first call's responses.
	if _, err := conn.CallTool(ctx, params); err != nil {
		t.Fatalf("CallTool() reuse error = %v", err)
	}
	if got := elicitations.Load(); got != 2 {
		t.Errorf("elicitations after reuse = %d, want 2 (stale responses must not be replayed)", got)
	}
}

// TestMultiRoundTrip_GetPrompt_AutoRetryDoesNotMutateCallerParams is the
// GetPrompt analogue of the CallTool caller-params test.
func TestMultiRoundTrip_GetPrompt_AutoRetryDoesNotMutateCallerParams(t *testing.T) {
	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddPrompt(&Prompt{Name: "review"}, func(_ context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &GetPromptResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Include sensitive data?"}},
				RequestState:  "prompt-state",
			}, nil
		}
		return &GetPromptResult{
			Messages: []*PromptMessage{{Role: "user", Content: &TextContent{Text: "review this code"}}},
		}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})

	params := &GetPromptParams{Name: "review"}
	res, err := conn.GetPrompt(ctx, params)
	if err != nil {
		t.Fatalf("GetPrompt() error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after auto-retry, want false")
	}
	if params.InputResponses != nil {
		t.Errorf("params.InputResponses = %v after GetPrompt, want nil (caller params must not be mutated)", params.InputResponses)
	}
	if params.RequestState != "" {
		t.Errorf("params.RequestState = %q after GetPrompt, want empty (caller params must not be mutated)", params.RequestState)
	}
}

// TestMultiRoundTrip_ReadResource_AutoRetryDoesNotMutateCallerParams is
// the ReadResource analogue of the CallTool caller-params test.
func TestMultiRoundTrip_ReadResource_AutoRetryDoesNotMutateCallerParams(t *testing.T) {
	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddResource(&Resource{URI: "test://data", Name: "data"}, func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &ReadResourceResult{
				InputRequests: InputRequestMap{"auth": &ElicitParams{Message: "Authenticate?"}},
				RequestState:  "resource-state",
			}, nil
		}
		return &ReadResourceResult{
			Contents: []*ResourceContents{{URI: "test://data", Text: "resource data"}},
		}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})

	params := &ReadResourceParams{URI: "test://data"}
	res, err := conn.ReadResource(ctx, params)
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after auto-retry, want false")
	}
	if params.InputResponses != nil {
		t.Errorf("params.InputResponses = %v after ReadResource, want nil (caller params must not be mutated)", params.InputResponses)
	}
	if params.RequestState != "" {
		t.Errorf("params.RequestState = %q after ReadResource, want empty (caller params must not be mutated)", params.RequestState)
	}
}

// wrappedCallToolParams embeds *CallToolParams so it satisfies Params and
// marshals identically, while being a concrete type the multi-round-trip
// retry does not recognize.
type wrappedCallToolParams struct{ *CallToolParams }

// TestMultiRoundTrip_UnsupportedRetryParamsType verifies that the client
// retry loop reports an explicit error when the params carry a type it
// cannot build retry params for, instead of silently resending the request
// unchanged until the retry cap.
func TestMultiRoundTrip_UnsupportedRetryParamsType(t *testing.T) {
	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	AddTool(srv, &Tool{Name: "act"}, func(ctx context.Context, req *CallToolRequest, input struct{}) (*CallToolResult, any, error) {
		return &CallToolResult{
			InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Sure?"}},
			RequestState:  "state-1",
		}, nil, nil
	})

	st, ct := NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})
	// Sending middleware added here wraps outside the multi-round-trip
	// middleware installed at client construction, so the retry loop sees
	// the substituted params type.
	c.AddSendingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method == methodCallTool {
				cs := req.GetSession().(*ClientSession)
				req = newClientRequest[Params](cs, wrappedCallToolParams{req.GetParams().(*CallToolParams)})
			}
			return next(ctx, method, req)
		}
	})
	cs, err := c.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	_, err = cs.CallTool(ctx, &CallToolParams{Name: "act"})
	if err == nil || !strings.Contains(err.Error(), "unsupported params type") {
		t.Fatalf("CallTool() error = %v, want unsupported params type error", err)
	}
}

func TestMultiRoundTrip_MaxRetries(t *testing.T) {
	testCases := []struct {
		name        string
		requests    InputRequestMap
		wantRetries int
	}{
		{
			name:        "load shedding",
			requests:    InputRequestMap{},
			wantRetries: maxLoadSheddingMultiRoundTripRetries,
		},
		{
			name:        "input request",
			requests:    InputRequestMap{"confirm": &ElicitParams{Message: "Again?"}},
			wantRetries: maxMultiRoundTripRetries,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			var serverCalls atomic.Int32
			srv := NewServer(testImpl, nil)
			AddTool(srv, &Tool{Name: "loop"}, func(ctx context.Context, req *CallToolRequest, input struct{}) (*CallToolResult, any, error) {
				serverCalls.Add(1)
				return &CallToolResult{InputRequests: tc.requests, RequestState: "loop-state"}, nil, nil
			})

			conn := mustConnect(t, srv, &ClientOptions{
				ElicitationHandler: func(_ context.Context, req *ElicitRequest) (*ElicitResult, error) {
					return &ElicitResult{Action: "accept"}, nil
				},
			})

			_, err := conn.CallTool(ctx, &CallToolParams{Name: "loop"})
			if err == nil {
				t.Fatal("CallTool() err = nil, want error for exceeded max retries")
			}
			if serverCalls.Load() != int32(tc.wantRetries) {
				t.Errorf("serverCalls = %d, want %d", serverCalls.Load(), tc.wantRetries)
			}
		})
	}
}

func TestMultiRoundTrip_ServerMiddleware(t *testing.T) {
	// multiRoundTripToolHandler returns a ToolHandler (plain, non-generic) that requests
	// the given inputRequests on the first call and returns the fulfilled
	// response IDs on the second.
	multiRoundTripToolHandler := func(inputRequests InputRequestMap) ToolHandler {
		return func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
			if len(req.Params.InputResponses) == 0 {
				return &CallToolResult{
					InputRequests: inputRequests,
					RequestState:  "state-1",
				}, nil
			}
			var ids []string
			for id := range req.Params.InputResponses {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			content := &TextContent{Text: fmt.Sprintf("%v", ids)}
			return &CallToolResult{Content: []Content{content}}, nil
		}
	}

	tests := []struct {
		name          string
		inputRequests InputRequestMap
		wantText      string
	}{
		{
			name: "elicit via ToolHandler",
			inputRequests: InputRequestMap{
				"confirm": &ElicitParams{Message: "Sure?"},
			},
			wantText: "[confirm]",
		},
		{
			name: "elicit and listRoots via ToolHandler",
			inputRequests: InputRequestMap{
				"confirm": &ElicitParams{Message: "OK?"},
				"roots":   &ListRootsParams{},
			},
			wantText: "[confirm roots]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			srv := NewServer(testImpl, nil)
			srv.AddTool(
				&Tool{Name: "act", InputSchema: &jsonschema.Schema{Type: "object"}},
				multiRoundTripToolHandler(tt.inputRequests),
			)

			// Connect with an OLD protocol version where multi-round-trip is not supported.
			// The server middleware should handle it transparently.
			st, ct := NewInMemoryTransports()
			ss, err := srv.Connect(t.Context(), st, nil)
			if err != nil {
				t.Fatalf("server.Connect() error = %v", err)
			}
			t.Cleanup(func() { _ = ss.Close() })

			c := NewClient(testImpl, &ClientOptions{
				MultiRoundTrip: &MultiRoundTripOptions{Disabled: true},
				ElicitationHandler: func(_ context.Context, req *ElicitRequest) (*ElicitResult, error) {
					return &ElicitResult{Action: "accept"}, nil
				},
			})
			c.AddRoots(&Root{URI: "file:///workspace", Name: "workspace"})
			cs, err := c.Connect(t.Context(), ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20251125})
			if err != nil {
				t.Fatalf("client.Connect() error = %v", err)
			}
			t.Cleanup(func() { _ = cs.Close() })

			res, err := cs.CallTool(ctx, &CallToolParams{Name: "act"})
			if err != nil {
				t.Fatalf("CallTool() error = %v", err)
			}
			if got := res.Content[0].(*TextContent).Text; got != tt.wantText {
				t.Errorf("result text = %q, want %q", got, tt.wantText)
			}
		})
	}
}

// TestMultiRoundTrip_ServerMiddleware_GetPrompt verifies the server-side
// shim retry for prompts: the rebuilt retry request must keep its concrete
// *ServerRequest[*GetPromptParams] type for the receiving dispatch.
func TestMultiRoundTrip_ServerMiddleware_GetPrompt(t *testing.T) {
	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddPrompt(&Prompt{Name: "review"}, func(_ context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &GetPromptResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Sure?"}},
				RequestState:  "prompt-state",
			}, nil
		}
		return &GetPromptResult{
			Messages: []*PromptMessage{{Role: "user", Content: &TextContent{Text: "approved"}}},
		}, nil
	})

	conn := mustConnectOldProtocol(t, srv, &ClientOptions{
		MultiRoundTrip: &MultiRoundTripOptions{Disabled: true},
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})

	res, err := conn.GetPrompt(ctx, &GetPromptParams{Name: "review"})
	if err != nil {
		t.Fatalf("GetPrompt() error = %v", err)
	}
	if got := res.Messages[0].Content.(*TextContent).Text; got != "approved" {
		t.Errorf("message text = %q, want %q", got, "approved")
	}
}

func TestMultiRoundTrip_GetPrompt_AutoRetry(t *testing.T) {

	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddPrompt(&Prompt{Name: "review"}, func(_ context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &GetPromptResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Include sensitive data?"}},
				RequestState:  "prompt-state",
			}, nil
		}
		return &GetPromptResult{
			Description: "Code review prompt",
			Messages:    []*PromptMessage{{Role: "user", Content: &TextContent{Text: "review this code"}}},
		}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})

	res, err := conn.GetPrompt(ctx, &GetPromptParams{Name: "review"})
	if err != nil {
		t.Fatalf("GetPrompt() error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after auto-retry, want false")
	}
	if len(res.Messages) != 1 {
		t.Fatalf("len(res.Messages) = %d, want 1", len(res.Messages))
	}
	if got := res.Messages[0].Content.(*TextContent).Text; got != "review this code" {
		t.Errorf("message text = %q, want %q", got, "review this code")
	}
}

func TestMultiRoundTrip_GetPrompt_ManualRetry(t *testing.T) {

	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddPrompt(&Prompt{Name: "review"}, func(_ context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &GetPromptResult{
				InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Include sensitive data?"}},
				RequestState:  "prompt-state",
			}, nil
		}
		return &GetPromptResult{
			Description: "Code review prompt",
			Messages:    []*PromptMessage{{Role: "user", Content: &TextContent{Text: "review this code"}}},
		}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		MultiRoundTrip: &MultiRoundTripOptions{Disabled: true},
	})

	res, err := conn.GetPrompt(ctx, &GetPromptParams{Name: "review"})
	if err != nil {
		t.Fatalf("GetPrompt() error = %v", err)
	}
	if !res.NeedsInput() {
		t.Fatal("NeedsInput() = false, want true")
	}
	if _, ok := res.InputRequests["confirm"].(*ElicitParams); !ok {
		t.Fatalf("InputRequests[confirm] type = %T, want *ElicitParams", res.InputRequests["confirm"])
	}

	res, err = conn.GetPrompt(ctx, &GetPromptParams{
		Name:           "review",
		InputResponses: InputResponseMap{"confirm": &ElicitResult{Action: "accept"}},
		RequestState:   res.RequestState,
	})
	if err != nil {
		t.Fatalf("GetPrompt() follow-up error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after follow-up, want false")
	}
	if len(res.Messages) != 1 {
		t.Fatalf("len(res.Messages) = %d, want 1", len(res.Messages))
	}
}

func TestMultiRoundTrip_ReadResource_AutoRetry(t *testing.T) {

	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddResource(&Resource{URI: "test://data", Name: "data"}, func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &ReadResourceResult{
				InputRequests: InputRequestMap{"auth": &ElicitParams{Message: "Authenticate?"}},
				RequestState:  "resource-state",
			}, nil
		}
		return &ReadResourceResult{
			Contents: []*ResourceContents{{URI: "test://data", Text: "resource data"}},
		}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, _ *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})

	res, err := conn.ReadResource(ctx, &ReadResourceParams{URI: "test://data"})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after auto-retry, want false")
	}
	if len(res.Contents) != 1 {
		t.Fatalf("len(res.Contents) = %d, want 1", len(res.Contents))
	}
	if got := res.Contents[0].Text; got != "resource data" {
		t.Errorf("resource text = %q, want %q", got, "resource data")
	}
}

func TestMultiRoundTrip_ReadResource_ManualRetry(t *testing.T) {

	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	srv.AddResource(&Resource{URI: "test://data", Name: "data"}, func(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
		if len(req.Params.InputResponses) == 0 {
			return &ReadResourceResult{
				InputRequests: InputRequestMap{"auth": &ElicitParams{Message: "Authenticate?"}},
				RequestState:  "resource-state",
			}, nil
		}
		return &ReadResourceResult{
			Contents: []*ResourceContents{{URI: "test://data", Text: "resource data"}},
		}, nil
	})

	conn := mustConnect(t, srv, &ClientOptions{
		MultiRoundTrip: &MultiRoundTripOptions{Disabled: true},
	})

	res, err := conn.ReadResource(ctx, &ReadResourceParams{URI: "test://data"})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if !res.NeedsInput() {
		t.Fatal("NeedsInput() = false, want true")
	}
	if _, ok := res.InputRequests["auth"].(*ElicitParams); !ok {
		t.Fatalf("InputRequests[auth] type = %T, want *ElicitParams", res.InputRequests["auth"])
	}

	res, err = conn.ReadResource(ctx, &ReadResourceParams{
		URI:            "test://data",
		InputResponses: InputResponseMap{"auth": &ElicitResult{Action: "accept"}},
		RequestState:   res.RequestState,
	})
	if err != nil {
		t.Fatalf("ReadResource() follow-up error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after follow-up, want false")
	}
	if len(res.Contents) != 1 {
		t.Fatalf("len(res.Contents) = %d, want 1", len(res.Contents))
	}
}

// TestSetMultiRoundTripRetryParams pins the properties the retry-request
// builder must uphold: the original params are never mutated, the copy
// carries the responses and state, the server-side rebuild preserves the
// request's concrete *ServerRequest[P] type (the receiving dispatch
// type-asserts on it) and its Extra, and an unrecognized params type is an
// explicit error. The server readResource case matters here because the
// end-to-end shim path cannot reach it (the read handler's nil-contents
// validation rejects input-required results for old-protocol clients).
func TestSetMultiRoundTripRetryParams(t *testing.T) {
	ss := &ServerSession{server: NewServer(testImpl, nil)}
	extra := &RequestExtra{}
	responses := InputResponseMap{"confirm": &ElicitResult{Action: "accept"}}

	// checkRetry asserts the returned params carry the retry fields while
	// the original params were left untouched.
	checkRetry := func(t *testing.T, orig, retry Params) {
		t.Helper()
		switch p := retry.(type) {
		case *CallToolParams:
			if len(p.InputResponses) != 1 || p.RequestState != "s1" {
				t.Errorf("retry params = %+v, want responses and state carried", p)
			}
		case *CallToolParamsRaw:
			if len(p.InputResponses) != 1 || p.RequestState != "s1" {
				t.Errorf("retry params = %+v, want responses and state carried", p)
			}
		case *GetPromptParams:
			if len(p.InputResponses) != 1 || p.RequestState != "s1" {
				t.Errorf("retry params = %+v, want responses and state carried", p)
			}
		case *ReadResourceParams:
			if len(p.InputResponses) != 1 || p.RequestState != "s1" {
				t.Errorf("retry params = %+v, want responses and state carried", p)
			}
		default:
			t.Errorf("retry params type = %T, want one of the multi-round-trip params types", p)
		}
		switch p := orig.(type) {
		case *CallToolParams:
			if p.InputResponses != nil || p.RequestState != "" {
				t.Errorf("original params mutated: %+v", p)
			}
		case *CallToolParamsRaw:
			if p.InputResponses != nil || p.RequestState != "" {
				t.Errorf("original params mutated: %+v", p)
			}
		case *GetPromptParams:
			if p.InputResponses != nil || p.RequestState != "" {
				t.Errorf("original params mutated: %+v", p)
			}
		case *ReadResourceParams:
			if p.InputResponses != nil || p.RequestState != "" {
				t.Errorf("original params mutated: %+v", p)
			}
		}
	}

	t.Run("server callTool", func(t *testing.T) {
		orig := &CallToolParamsRaw{Name: "act"}
		req, err := setMultiRoundTripRetryParams(methodCallTool, &ServerRequest[*CallToolParamsRaw]{Session: ss, Params: orig, Extra: extra}, responses, "s1")
		if err != nil {
			t.Fatalf("setMultiRoundTripRetryParams() error = %v", err)
		}
		got, ok := req.(*ServerRequest[*CallToolParamsRaw])
		if !ok {
			t.Fatalf("retry request type = %T, want *ServerRequest[*CallToolParamsRaw]", req)
		}
		if got.Extra != extra {
			t.Error("retry request lost Extra")
		}
		checkRetry(t, orig, got.Params)
	})

	t.Run("server getPrompt", func(t *testing.T) {
		orig := &GetPromptParams{Name: "review"}
		req, err := setMultiRoundTripRetryParams(methodGetPrompt, &ServerRequest[*GetPromptParams]{Session: ss, Params: orig, Extra: extra}, responses, "s1")
		if err != nil {
			t.Fatalf("setMultiRoundTripRetryParams() error = %v", err)
		}
		got, ok := req.(*ServerRequest[*GetPromptParams])
		if !ok {
			t.Fatalf("retry request type = %T, want *ServerRequest[*GetPromptParams]", req)
		}
		if got.Extra != extra {
			t.Error("retry request lost Extra")
		}
		checkRetry(t, orig, got.Params)
	})

	t.Run("server readResource", func(t *testing.T) {
		orig := &ReadResourceParams{URI: "test://data"}
		req, err := setMultiRoundTripRetryParams(methodReadResource, &ServerRequest[*ReadResourceParams]{Session: ss, Params: orig, Extra: extra}, responses, "s1")
		if err != nil {
			t.Fatalf("setMultiRoundTripRetryParams() error = %v", err)
		}
		got, ok := req.(*ServerRequest[*ReadResourceParams])
		if !ok {
			t.Fatalf("retry request type = %T, want *ServerRequest[*ReadResourceParams]", req)
		}
		if got.Extra != extra {
			t.Error("retry request lost Extra")
		}
		checkRetry(t, orig, got.Params)
	})

	t.Run("client params", func(t *testing.T) {
		for _, orig := range []Params{
			&CallToolParams{Name: "act"},
			&GetPromptParams{Name: "review"},
			&ReadResourceParams{URI: "test://data"},
		} {
			req, err := setMultiRoundTripRetryParams(methodCallTool, newClientRequest[Params](nil, orig), responses, "s1")
			if err != nil {
				t.Fatalf("setMultiRoundTripRetryParams(%T) error = %v", orig, err)
			}
			got, ok := req.(*ClientRequest[Params])
			if !ok {
				t.Fatalf("retry request type = %T, want *ClientRequest[Params]", req)
			}
			checkRetry(t, orig, got.Params)
		}
	})

	t.Run("unsupported params type", func(t *testing.T) {
		wrapped := wrappedCallToolParams{&CallToolParams{Name: "act"}}
		_, err := setMultiRoundTripRetryParams(methodCallTool, newClientRequest[Params](nil, wrapped), responses, "s1")
		if err == nil || !strings.Contains(err.Error(), "unsupported params type") {
			t.Fatalf("setMultiRoundTripRetryParams() error = %v, want unsupported params type error", err)
		}
	})
}

func mustConnect(t *testing.T, s *Server, clientOpts *ClientOptions) *ClientSession {
	t.Helper()

	st, ct := NewInMemoryTransports()
	ss, err := s.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = ss.Close()
	})

	c := NewClient(testImpl, clientOpts)
	cs, err := c.Connect(t.Context(), ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}

// mustConnectOldProtocol is mustConnect for a client on a protocol version
// that predates multi-round-trip: the server-side shim, not the client
// middleware, fulfills input requests.
func mustConnectOldProtocol(t *testing.T, s *Server, clientOpts *ClientOptions) *ClientSession {
	t.Helper()

	st, ct := NewInMemoryTransports()
	ss, err := s.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = ss.Close()
	})

	c := NewClient(testImpl, clientOpts)
	cs, err := c.Connect(t.Context(), ct, &ClientSessionOptions{ProtocolVersion: protocolVersion20251125})
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}
