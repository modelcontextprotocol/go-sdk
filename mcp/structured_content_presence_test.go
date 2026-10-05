// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStructuredContentPresenceAcrossTransport(t *testing.T) {
	for _, payload := range []string{"", "null", "9007199254740993", `{"n":18446744073709551615}`, `[null,9007199254740993]`, "false", `"text"`} {
		t.Run(payload, func(t *testing.T) {
			serverTransport, clientTransport := NewInMemoryTransports()
			server := NewServer(&Implementation{Name: "server", Version: "1"}, nil)
			server.AddTool(&Tool{Name: "result", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *CallToolRequest) (*CallToolResult, error) {
				result := &CallToolResult{}
				if payload != "" {
					result.StructuredContent = json.RawMessage(payload)
				}
				return result, nil
			})
			serverSession, err := server.Connect(t.Context(), serverTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer serverSession.Close()
			client := NewClient(&Implementation{Name: "client", Version: "1"}, nil)
			session, err := client.Connect(t.Context(), clientTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			result, err := session.CallTool(t.Context(), &CallToolParams{Name: "result"})
			if err != nil {
				t.Fatal(err)
			}
			if payload == "" {
				if result.StructuredContent != nil {
					t.Fatalf("omitted content = %#v", result.StructuredContent)
				}
				return
			}
			if result.StructuredContent == nil {
				t.Fatal("present content became absent")
			}
			encoded, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != payload {
				t.Fatalf("structured content = %s; want %s", encoded, payload)
			}
		})
	}
}
