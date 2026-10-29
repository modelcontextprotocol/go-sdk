// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestInjectRequestMetaPreservesParams(t *testing.T) {
	cs := &ClientSession{
		client: NewClient(testImpl, nil),
		state: clientSessionState{InitializeResult: &InitializeResult{
			ProtocolVersion: protocolVersion20260728,
		}},
	}
	for _, meta := range []Meta{
		nil,
		{},
		{"custom": "value"},
		{MetaKeyProtocolVersion: "override", MetaKeyClientInfo: "client", MetaKeyClientCapabilities: "capabilities"},
	} {
		params := &GetPromptParams{Name: "prompt", Meta: meta}
		wantMeta := maps.Clone(meta)
		got := injectRequestMeta(cs, params)
		if diff := cmp.Diff(wantMeta, params.Meta); diff != "" {
			t.Errorf("caller metadata changed (-want +got):\n%s", diff)
		}
		if got == params {
			t.Error("injectRequestMeta returned caller-owned params")
		}
		if got.Name != params.Name {
			t.Error("prompt name changed")
		}
		for _, key := range []string{MetaKeyProtocolVersion, MetaKeyClientInfo, MetaKeyClientCapabilities} {
			if _, ok := got.Meta[key]; !ok {
				t.Errorf("missing metadata key %q", key)
			}
			if value, ok := wantMeta[key]; ok && !cmp.Equal(value, got.Meta[key]) {
				t.Errorf("explicit metadata key %q was overwritten", key)
			}
		}
		got.Meta["custom"] = "changed"
		if diff := cmp.Diff(wantMeta, params.Meta); diff != "" {
			t.Errorf("metadata map is shared (-want +got):\n%s", diff)
		}
	}
	if got := injectRequestMeta(cs, (*GetPromptParams)(nil)); got == nil || got.Meta == nil {
		t.Error("nil params did not receive metadata")
	}
}

func TestCallToolPreservesSharedParams(t *testing.T) {
	for _, version := range []string{protocolVersion20251125, protocolVersion20260728} {
		t.Run(version, func(t *testing.T) {
			ctx := t.Context()
			s := NewServer(testImpl, nil)
			s.AddTool(&Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
				func(_ context.Context, req *CallToolRequest) (*CallToolResult, error) {
					name := "legacy"
					if info, ok := req.Params.Meta[MetaKeyClientInfo].(map[string]any); ok {
						name, _ = info["name"].(string)
					}
					return &CallToolResult{Content: []Content{&TextContent{Text: name}}}, nil
				})
			var clients []*ClientSession
			for _, name := range []string{"client-a", "client-b"} {
				ct, st := NewInMemoryTransports()
				ss, err := s.Connect(ctx, st, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = ss.Close() })
				cs, err := NewClient(&Implementation{Name: name, Version: "1"}, nil).Connect(ctx, ct,
					&ClientSessionOptions{ProtocolVersion: version})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cs.Close() })
				clients = append(clients, cs)
			}
			call := func(cs *ClientSession, params *CallToolParams) {
				t.Helper()
				result, err := cs.CallTool(ctx, params)
				if err != nil {
					t.Error(err)
					return
				}
				want := "legacy"
				if version == protocolVersion20260728 {
					want = cs.client.impl.Name
				}
				if got := result.Content[0].(*TextContent).Text; got != want {
					t.Errorf("server received client name %q, want %q", got, want)
				}
			}
			for _, concurrent := range []bool{false, true} {
				for _, meta := range []Meta{nil, {"custom": "value"}} {
					params := &CallToolParams{Name: "ping", Meta: meta}
					want := &CallToolParams{Name: "ping", Meta: maps.Clone(meta)}
					if concurrent {
						var wg sync.WaitGroup
						for range 4 {
							for _, cs := range clients {
								wg.Go(func() { call(cs, params) })
							}
						}
						wg.Wait()
					} else {
						for _, cs := range clients {
							call(cs, params)
						}
					}
					if diff := cmp.Diff(want, params); diff != "" {
						t.Errorf("caller params changed (-want +got):\n%s", diff)
					}
				}
			}
		})
	}
}
