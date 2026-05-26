// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type deployResult struct {
	Deployed bool   `json:"deployed"`
	Reason   string `json:"reason,omitempty"`
}

func TestMRTR_ManualRetry(t *testing.T) {
	orig := supportedProtocolVersions
	supportedProtocolVersions = append(slices.Clone(orig), protocolVersion20260630)
	t.Cleanup(func() { supportedProtocolVersions = orig })

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

	conn := mustConnectMRTR(t, srv, &ClientOptions{
		MRTR: &MRTROptions{Disabled: true},
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

func TestMRTR_AutoRetry(t *testing.T) {
	orig := supportedProtocolVersions
	supportedProtocolVersions = append(slices.Clone(orig), protocolVersion20260630)
	t.Cleanup(func() { supportedProtocolVersions = orig })

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
			return nil, nil, nil
		}
		elicitResult := resp.(*ElicitResult)
		if elicitResult == nil || elicitResult.Action != "accept" {
			return &CallToolResult{}, &deployResult{Deployed: false, Reason: "cancelled"}, nil
		}
		return &CallToolResult{}, &deployResult{Deployed: true}, nil
	})

	conn := mustConnectMRTR(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, req *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept", Content: map[string]any{"ok": true}}, nil
		},
	})

	res, err := conn.CallTool(ctx, &CallToolParams{Name: "deploy"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.NeedsInput() {
		t.Fatal("NeedsInput() = true after auto-retry, want false")
	}

	if diff := cmp.Diff(map[string]any{"deployed": true}, res.StructuredContent, ctrCmpOpts...); diff != "" {
		t.Errorf("result mismatch (-want +got):\n%s", diff)
	}
}

func TestMRTR_MaxRetries(t *testing.T) {
	orig := supportedProtocolVersions
	supportedProtocolVersions = append(slices.Clone(orig), protocolVersion20260630)
	t.Cleanup(func() { supportedProtocolVersions = orig })

	ctx := context.Background()

	srv := NewServer(testImpl, nil)
	AddTool(srv, &Tool{Name: "loop"}, func(ctx context.Context, req *CallToolRequest, input struct{}) (*CallToolResult, any, error) {
		return &CallToolResult{
			InputRequests: InputRequestMap{"confirm": &ElicitParams{Message: "Again?"}},
			RequestState:  "loop-state",
		}, nil, nil
	})

	conn := mustConnectMRTR(t, srv, &ClientOptions{
		ElicitationHandler: func(_ context.Context, req *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
		MRTR: &MRTROptions{MaxRetries: 2},
	})

	_, err := conn.CallTool(ctx, &CallToolParams{Name: "loop"})
	if err == nil {
		t.Fatal("CallTool() err = nil, want error for exceeded max retries")
	}
}

func mustConnectMRTR(t *testing.T, s *Server, clientOpts *ClientOptions) *ClientSession {
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
	cs, err := c.Connect(t.Context(), ct, &ClientSessionOptions{protocolVersion: protocolVersion20260630})
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}
