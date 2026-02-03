// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build go1.25

package mcp

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestElicitationCompleteNotification_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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

		_, ss, cleanup := basicClientServerConnection(t, c, nil, nil)
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
		gotParams := <-elicitationCompleteCh
		if gotParams.ElicitationID != elicitID {
			t.Errorf("elicitationComplete notification ID mismatch: got %q, want %q", gotParams.ElicitationID, elicitID)
		}
	})
}
