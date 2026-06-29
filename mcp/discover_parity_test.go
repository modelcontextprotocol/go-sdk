// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// postDiscover issues a SEP-2575 server/discover POST and decodes the result.
func postDiscover(t *testing.T, url string, into any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  methodDiscover,
		"params": map[string]any{
			"_meta": map[string]any{
				MetaKeyProtocolVersion:    protocolVersion20260728,
				MetaKeyClientInfo:         map[string]any{"name": "parity-client", "version": "v1"},
				MetaKeyClientCapabilities: map[string]any{},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(protocolVersionHeader, protocolVersion20260728)
	req.Header.Set(methodHeader, methodDiscover)
	postParity(t, req, methodDiscover, into)
}

// postInitialize issues a legacy initialize handshake POST and decodes the result.
func postInitialize(t *testing.T, url string, into any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  methodInitialize,
		"params": map[string]any{
			"protocolVersion": protocolVersion20251125,
			"clientInfo":      map[string]any{"name": "parity-client", "version": "v1"},
			"capabilities":    map[string]any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	postParity(t, req, methodInitialize, into)
}

func postParity(t *testing.T, req *http.Request, method string, into any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200; body = %s", method, resp.StatusCode, raw)
	}
	payload := raw
	if i := bytes.Index(raw, []byte("data: ")); i >= 0 {
		payload = raw[i+len("data: "):]
		if j := bytes.IndexByte(payload, '\n'); j >= 0 {
			payload = payload[:j]
		}
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.Unmarshal(payload, &rpc); err != nil {
		t.Fatalf("%s: unmarshal %q: %v", method, raw, err)
	}
	if rpc.Error != nil {
		t.Fatalf("%s: returned error: %v (body = %s)", method, rpc.Error, raw)
	}
	if err := json.Unmarshal(rpc.Result, into); err != nil {
		t.Fatalf("%s: unmarshal result: %v", method, err)
	}
}

// TestDiscoverInitializeInstructionsParity guards modelcontextprotocol/go-sdk#1034:
// server/discover must surface the same server-identity fields as initialize for a
// server configured with ServerOptions.Instructions, on both stateful and stateless
// streamable HTTP transports.
func TestDiscoverInitializeInstructionsParity(t *testing.T) {
	const instructions = "use the echo tool to repeat messages"

	for _, stateless := range []bool{false, true} {
		name := "stateful"
		if stateless {
			name = "stateless"
		}
		t.Run(name, func(t *testing.T) {
			server := NewServer(testImpl, &ServerOptions{Instructions: instructions})
			AddTool(server, &Tool{Name: "echo", Description: "echoes its input"},
				func(_ context.Context, _ *CallToolRequest, args struct {
					Msg string `json:"msg"`
				}) (*CallToolResult, struct{}, error) {
					return &CallToolResult{Content: []Content{&TextContent{Text: args.Msg}}}, struct{}{}, nil
				})
			handler := NewStreamableHTTPHandler(
				func(*http.Request) *Server { return server },
				&StreamableHTTPOptions{Stateless: stateless},
			)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			var init InitializeResult
			postInitialize(t, httpServer.URL, &init)
			var disc DiscoverResult
			postDiscover(t, httpServer.URL, &disc)

			if disc.Instructions != instructions {
				t.Errorf("DiscoverResult.Instructions = %q, want %q", disc.Instructions, instructions)
			}
			if init.Instructions != instructions {
				t.Errorf("InitializeResult.Instructions = %q, want %q", init.Instructions, instructions)
			}
			if disc.Instructions != init.Instructions {
				t.Errorf("instructions parity: discover %q != initialize %q", disc.Instructions, init.Instructions)
			}
			if !reflect.DeepEqual(disc.ServerInfo, init.ServerInfo) {
				t.Errorf("serverInfo parity: discover %+v != initialize %+v", disc.ServerInfo, init.ServerInfo)
			}
			if !reflect.DeepEqual(disc.Capabilities, init.Capabilities) {
				t.Errorf("capabilities parity: discover %+v != initialize %+v", disc.Capabilities, init.Capabilities)
			}
		})
	}
}
