// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// TODO: migrate other elicitation tests here.

func TestElicitationURLMode(t *testing.T) {
	ctx := context.Background()
	clientErr := errors.New("client failed to elicit")

	testCases := []struct {
		name             string
		handler          func(context.Context, *ElicitRequest) (*ElicitResult, error)
		params           *ElicitParams
		wantResultAction string
		wantErrMsg       string
		wantErrCode      int64
	}{
		{
			name: "success",
			handler: func(ctx context.Context, req *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{Action: "accept"}, nil
			},
			params: &ElicitParams{
				Mode:    "url",
				Message: "Please provide information via URL",
				URL:     "https://example.com/form",
			},
			wantResultAction: "accept",
		},
		{
			name: "decline",
			handler: func(ctx context.Context, req *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{Action: "decline"}, nil
			},
			params: &ElicitParams{
				Mode:    "url",
				Message: "Please provide information via URL",
				URL:     "https://example.com/form",
			},
			wantResultAction: "decline",
		},
		{
			name: "client error",
			handler: func(ctx context.Context, req *ElicitRequest) (*ElicitResult, error) {
				return nil, clientErr
			},
			params: &ElicitParams{
				Mode:    "url",
				Message: "This should fail",
				URL:     "https://example.com/form",
			},
			wantErrMsg: clientErr.Error(),
		},
		{
			name: "missing url",
			handler: func(ctx context.Context, req *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{Action: "accept"}, nil
			},
			params: &ElicitParams{
				Mode:    "url",
				Message: "URL is missing",
			},
			wantErrMsg:  "URL must be set for URL elicitation",
			wantErrCode: jsonrpc.CodeInvalidParams,
		},
		{
			name: "schema not allowed",
			handler: func(ctx context.Context, req *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{Action: "accept"}, nil
			},
			params: &ElicitParams{
				Mode:    "url",
				Message: "Schema is not allowed",
				URL:     "https://example.com/form",
				RequestedSchema: &jsonschema.Schema{
					Type: "object",
				},
			},
			wantErrMsg:  "requestedSchema must not be set for URL elicitation",
			wantErrCode: jsonrpc.CodeInvalidParams,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ct, st := NewInMemoryTransports()
			s := NewServer(testImpl, nil)
			ss, err := s.Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()

			c := NewClient(testImpl, &ClientOptions{
				Capabilities: &ClientCapabilities{
					Roots:   RootCapabilities{ListChanged: true},
					RootsV2: &RootCapabilities{ListChanged: true},
					Elicitation: &ElicitationCapabilities{
						URL: &URLElicitationCapabilities{},
					},
				},
				ElicitationHandler: tc.handler,
			})
			cs, err := c.Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()

			result, err := ss.Elicit(ctx, tc.params)

			if tc.wantErrMsg != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Fatalf("Elicit(...): got error %v, want containing %q", err, tc.wantErrMsg)
				}
				if tc.wantErrCode != 0 {
					if code := errorCode(err); code != tc.wantErrCode {
						t.Errorf("Elicit(...): got error code %d, want %d", code, tc.wantErrCode)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("Elicit failed: %v", err)
				}
				if result.Action != tc.wantResultAction {
					t.Errorf("Elicit(...): got action %q, want %q", result.Action, tc.wantResultAction)
				}
			}
		})
	}
}

func TestElicitationCompleteNotification(t *testing.T) {
	ctx := context.Background()

	var elicitationCompleteCh = make(chan *ElicitationCompleteParams, 1)

	c := NewClient(testImpl, &ClientOptions{
		Capabilities: &ClientCapabilities{
			Roots:   RootCapabilities{ListChanged: true},
			RootsV2: &RootCapabilities{ListChanged: true},
			Elicitation: &ElicitationCapabilities{
				URL: &URLElicitationCapabilities{},
			},
		},
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
		ElicitationCompleteHandler: func(_ context.Context, req *ElicitationCompleteNotificationRequest) {
			elicitationCompleteCh <- req.Params
		},
	})

	cs, ss, cleanup := basicClientServerConnection(t, c, nil, nil)
	_ = cs // Dummy usage to avoid "declared and not used" error.
	defer cleanup()

	// 1. Server initiates a URL elicitation
	elicitID := "testElicitationID-123"
	resp, err := ss.Elicit(ctx, &ElicitParams{
		Mode:          "url",
		Message:       "Please complete this form: ",
		URL:           "https://example.com/form?id=" + elicitID,
		ElicitationID: elicitID,
	})
	if err != nil {
		t.Fatalf("Elicit failed: %v", err)
	}
	if resp.Action != "accept" {
		t.Fatalf("Elicit action is %q, want %q", resp.Action, "accept")
	}

	// 2. Server sends elicitation complete notification (simulating out-of-band completion)
	err = handleNotify(ctx, notificationElicitationComplete, newServerRequest(ss, &ElicitationCompleteParams{
		ElicitationID: elicitID,
	}))
	if err != nil {
		t.Fatalf("failed to send elicitation complete notification: %v", err)
	}

	// 3. Client should receive the notification
	select {
	case gotParams := <-elicitationCompleteCh:
		if gotParams.ElicitationID != elicitID {
			t.Errorf("elicitationComplete notification ID mismatch: got %q, want %q", gotParams.ElicitationID, elicitID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for elicitation complete notification")
	}
}
