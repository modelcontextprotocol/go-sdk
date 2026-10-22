// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtocolNumberPrecisionAcrossTransport(t *testing.T) {
	for _, transport := range []string{"ndjson", "http"} {
		t.Run(transport, func(t *testing.T) {
			for _, literal := range []string{"9007199254740993", "-9007199254740993", "0.12345678901234567890123456789", "18446744073709551615"} {
				t.Run(literal, func(t *testing.T) {
					schema := json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"id":{"const":%s,"enum":[%s],"minimum":%s,"maximum":%s}},"required":["id"]}`, literal, literal, literal, literal))
					payload := json.RawMessage(fmt.Sprintf(`{"id":%s}`, literal))
					meta := Meta{"test/value": json.RawMessage(literal), "test/nested": json.RawMessage(fmt.Sprintf(`{"values":[%s]}`, literal))}
					server := NewServer(&Implementation{Name: "test", Version: "1"}, nil)
					server.AddTool(&Tool{Name: "exact", InputSchema: schema, OutputSchema: schema, Meta: meta}, func(context.Context, *CallToolRequest) (*CallToolResult, error) {
						return &CallToolResult{Meta: meta, StructuredContent: payload, Content: []Content{
							&TextContent{Text: "ok", Meta: meta},
							&ResourceLink{URI: "test://exact", Name: "exact", Meta: meta},
							&EmbeddedResource{Meta: meta, Resource: &ResourceContents{URI: "test://exact", Text: "ok", Meta: meta}},
						}}, nil
					})
					var clientTransport Transport
					if transport == "http" {
						hs := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil))
						defer hs.Close()
						clientTransport = &StreamableClientTransport{Endpoint: hs.URL}
					} else {
						st, ct := NewInMemoryTransports()
						ss, err := server.Connect(t.Context(), st, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer ss.Close()
						clientTransport = ct
					}
					client := NewClient(&Implementation{Name: "test", Version: "1"}, nil)
					cs, err := client.Connect(t.Context(), clientTransport, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer cs.Close()
					list, err := cs.ListTools(t.Context(), nil)
					if err != nil {
						t.Fatal(err)
					}
					result, err := cs.CallTool(t.Context(), &CallToolParams{Name: "exact", Arguments: payload})
					if err != nil {
						t.Fatal(err)
					}
					check := func(name string, value any, want string) {
						t.Helper()
						t.Run(name, func(t *testing.T) {
							got, err := json.Marshal(value)
							if err != nil {
								t.Fatal(err)
							}
							if string(got) != want {
								t.Errorf("received %s; want %s", got, want)
							}
						})
					}
					for name, s := range map[string]any{"input_schema": list.Tools[0].InputSchema, "output_schema": list.Tools[0].OutputSchema} {
						constraints := s.(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)
						for _, keyword := range []string{"const", "minimum", "maximum"} {
							check(name+"_"+keyword, constraints[keyword], literal)
						}
						check(name+"_enum", constraints["enum"], "["+literal+"]")
					}
					for name, m := range map[string]Meta{
						"tool_meta":     list.Tools[0].Meta,
						"result_meta":   result.Meta,
						"text_meta":     result.Content[0].(*TextContent).Meta,
						"link_meta":     result.Content[1].(*ResourceLink).Meta,
						"embedded_meta": result.Content[2].(*EmbeddedResource).Meta,
						"resource_meta": result.Content[2].(*EmbeddedResource).Resource.Meta,
					} {
						check(name, m["test/value"], literal)
						check(name+"_nested", m["test/nested"], fmt.Sprintf(`{"values":[%s]}`, literal))
					}
					check("structured_content_control", result.StructuredContent, string(payload))
				})
			}
		})
	}
}

func TestToolNumberPrecision(t *testing.T) {
	const data = `{"name":"exact","inputSchema":{"type":"object","properties":{"id":{"const":1e400}}},"_meta":{"revision":9007199254740993}}`
	var tool Tool
	if err := json.Unmarshal([]byte(data), &tool); err != nil {
		t.Fatal(err)
	}
	got := tool.InputSchema.(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)["const"]
	if got != json.Number("1e400") {
		t.Errorf("const = %v (%T), want json.Number(1e400)", got, got)
	}
	if got := tool.Meta["revision"]; got != json.Number("9007199254740993") {
		t.Errorf("revision = %v (%T), want exact json.Number", got, got)
	}
	if tool.Name != "exact" || tool.OutputSchema != nil {
		t.Errorf("unexpected tool fields: %+v", tool)
	}
}
