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
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// exactNumbers are JSON numbers that do not survive a float64 round trip:
// the integers and the long decimal lose precision, and the exponent form is
// rewritten.
var exactNumbers = []string{
	"9007199254740993", // 2^53 + 1
	"-9007199254740993",
	"18446744073709551615", // 2^64 - 1
	"0.12345678901234567890123456789",
	"1.2300e+12",
}

// numericSchema returns a form schema with n in several numeric keywords.
func numericSchema(n string) string {
	return fmt.Sprintf(`{"type":"object","properties":{"n":{"type":"number","minimum":%[1]s,"maximum":%[1]s,"enum":[%[1]s],"default":%[1]s}}}`, n)
}

// numericMeta returns metadata with n at the top level and nested.
func numericMeta(n string) string {
	return fmt.Sprintf(`{"n":%[1]s,"nested":{"values":[%[1]s]}}`, n)
}

// decodeExactly decodes data with numbers as json.Number, the representation
// the SDK is expected to produce.
func decodeExactly(t *testing.T, data string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// checkExact reports whether got holds the value of the JSON text want, with
// every number represented as the same json.Number.
func checkExact(t *testing.T, what string, got any, want string) {
	t.Helper()
	if m, ok := got.(Meta); ok {
		got = map[string]any(m)
	}
	if diff := cmp.Diff(decodeExactly(t, want), got); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", what, diff)
	}
}

func TestSchemaAndMetaNumbersDecodeExactly(t *testing.T) {
	// 1e400 is outside the range of float64, so before numbers were decoded
	// as json.Number it made the whole message undecodable.
	for _, n := range slices.Concat(exactNumbers, []string{"1e400"}) {
		schema, meta := numericSchema(n), numericMeta(n)
		tool := fmt.Sprintf(`{"name":"exact","inputSchema":%[1]s,"outputSchema":%[1]s,"_meta":%[2]s}`, schema, meta)
		elicit := fmt.Sprintf(`{"mode":"form","message":"m","requestedSchema":%s,"_meta":%s}`, schema, meta)
		checkTool := func(t *testing.T, got *Tool) {
			t.Helper()
			checkExact(t, "InputSchema", got.InputSchema, schema)
			checkExact(t, "OutputSchema", got.OutputSchema, schema)
			checkExact(t, "Meta", got.Meta, meta)
		}
		checkElicit := func(t *testing.T, got *ElicitParams) {
			t.Helper()
			checkExact(t, "RequestedSchema", got.RequestedSchema, schema)
			checkExact(t, "Meta", got.Meta, meta)
		}
		unmarshal := func(t *testing.T, data string, v any) {
			t.Helper()
			if err := json.Unmarshal([]byte(data), v); err != nil {
				t.Fatal(err)
			}
		}

		t.Run(n, func(t *testing.T) {
			t.Run("Tool", func(t *testing.T) {
				var got Tool
				unmarshal(t, tool, &got)
				checkTool(t, &got)
			})
			t.Run("ListToolsResult", func(t *testing.T) {
				var got ListToolsResult
				unmarshal(t, `{"tools":[`+tool+`]}`, &got)
				checkTool(t, got.Tools[0])
			})
			t.Run("CreateMessageWithToolsParams", func(t *testing.T) {
				var got CreateMessageWithToolsParams
				unmarshal(t, `{"maxTokens":1,"messages":[],"tools":[`+tool+`]}`, &got)
				checkTool(t, got.Tools[0])
			})
			t.Run("CallToolResult", func(t *testing.T) {
				var got CallToolResult
				unmarshal(t, fmt.Sprintf(`{"_meta":%[1]s,"content":[`+
					`{"type":"text","text":"t","_meta":%[1]s},`+
					`{"type":"resource_link","uri":"u","name":"n","_meta":%[1]s},`+
					`{"type":"resource","resource":{"uri":"u","text":"t","_meta":%[1]s},"_meta":%[1]s}]}`, meta), &got)
				checkExact(t, "Meta", got.Meta, meta)
				checkExact(t, "TextContent.Meta", got.Content[0].(*TextContent).Meta, meta)
				checkExact(t, "ResourceLink.Meta", got.Content[1].(*ResourceLink).Meta, meta)
				embedded := got.Content[2].(*EmbeddedResource)
				checkExact(t, "EmbeddedResource.Meta", embedded.Meta, meta)
				checkExact(t, "ResourceContents.Meta", embedded.Resource.Meta, meta)
			})
			t.Run("ElicitParams", func(t *testing.T) {
				var got ElicitParams
				unmarshal(t, elicit, &got)
				checkElicit(t, &got)
			})
			t.Run("InputRequestMap", func(t *testing.T) {
				var got InputRequestMap
				unmarshal(t, `{"r":{"method":"elicitation/create","params":`+elicit+`}}`, &got)
				params, ok := got["r"].(*ElicitParams)
				if !ok {
					t.Fatalf("input request is %T, want *ElicitParams", got["r"])
				}
				checkElicit(t, params)
			})
		})
	}
}

func TestSchemaAndMetaFloat64Compatibility(t *testing.T) {
	prev := schemaandmetafloat64
	schemaandmetafloat64 = "1"
	t.Cleanup(func() { schemaandmetafloat64 = prev })

	const n = "9007199254740993"
	const rounded = float64(9007199254740992)
	schema, meta := numericSchema(n), numericMeta(n)
	minimum := func(schema any) any {
		return schema.(map[string]any)["properties"].(map[string]any)["n"].(map[string]any)["minimum"]
	}
	check := func(what string, got any) {
		t.Helper()
		if got != rounded {
			t.Errorf("%s = %v (%T), want rounded float64 value", what, got, got)
		}
	}

	var tool Tool
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"name":"t","inputSchema":%[1]s,"outputSchema":%[1]s,"_meta":%[2]s}`, schema, meta)), &tool); err != nil {
		t.Fatal(err)
	}
	check("Tool.InputSchema", minimum(tool.InputSchema))
	check("Tool.OutputSchema", minimum(tool.OutputSchema))
	check("Tool.Meta", tool.Meta["n"])

	var result CallToolResult
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"_meta":%[1]s,"content":[{"type":"text","text":"t","_meta":%[1]s}],"structuredContent":{"n":%[2]s}}`, meta, n)), &result); err != nil {
		t.Fatal(err)
	}
	check("CallToolResult.Meta", result.Meta["n"])
	check("TextContent.Meta", result.Content[0].(*TextContent).Meta["n"])
	// StructuredContent has its own compatibility parameter.
	if got := result.StructuredContent.(map[string]any)["n"]; got != json.Number(n) {
		t.Errorf("StructuredContent = %v (%T), want json.Number", got, got)
	}

	var elicit ElicitParams
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"message":"m","requestedSchema":%s,"_meta":%s}`, schema, meta)), &elicit); err != nil {
		t.Fatal(err)
	}
	check("ElicitParams.RequestedSchema", minimum(elicit.RequestedSchema))
	check("ElicitParams.Meta", elicit.Meta["n"])
}

// TestSchemaAndMetaDecodeInPlace checks that Tool and ElicitParams still
// decode the way encoding/json decodes a struct: fields absent from the input
// keep their values, and null leaves the value unchanged.
func TestSchemaAndMetaDecodeInPlace(t *testing.T) {
	tool := Tool{Name: "kept", Description: "old"}
	if err := json.Unmarshal([]byte(`{"description":"new"}`), &tool); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`null`), &tool); err != nil {
		t.Fatal(err)
	}
	if want := (Tool{Name: "kept", Description: "new"}); !cmp.Equal(tool, want) {
		t.Errorf("Tool = %+v, want %+v", tool, want)
	}

	elicit := ElicitParams{Mode: "form", Message: "old"}
	if err := json.Unmarshal([]byte(`{"message":"new"}`), &elicit); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`null`), &elicit); err != nil {
		t.Fatal(err)
	}
	if want := (ElicitParams{Mode: "form", Message: "new"}); !cmp.Equal(elicit, want) {
		t.Errorf("ElicitParams = %+v, want %+v", elicit, want)
	}
}

// TestSchemaAndMetaNumbersAcrossTransports checks the numbers a client
// receives in tools/list, tools/call and elicitation/create.
func TestSchemaAndMetaNumbersAcrossTransports(t *testing.T) {
	const n = "9007199254740993"
	schema, meta := numericSchema(n), numericMeta(n)
	rawMeta := func() Meta {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(meta), &m); err != nil {
			t.Fatal(err)
		}
		out := Meta{}
		for k, v := range m {
			out[k] = v
		}
		return out
	}

	newServer := func() *Server {
		server := NewServer(testImpl, nil)
		server.AddTool(&Tool{
			Name:         "exact",
			InputSchema:  json.RawMessage(schema),
			OutputSchema: json.RawMessage(schema),
			Meta:         rawMeta(),
		}, func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
			if _, err := req.Session.Elicit(ctx, &ElicitParams{
				Mode:            "form",
				Message:         "m",
				RequestedSchema: json.RawMessage(schema),
				Meta:            rawMeta(),
			}); err != nil {
				return nil, err
			}
			return &CallToolResult{
				Meta:              rawMeta(),
				Content:           []Content{&TextContent{Text: "t", Meta: rawMeta()}},
				StructuredContent: json.RawMessage(`{"n":` + n + `}`),
			}, nil
		})
		return server
	}

	transports := map[string]func(t *testing.T, server *Server) Transport{
		"in-memory": func(t *testing.T, server *Server) Transport {
			ct, st := NewInMemoryTransports()
			ss, err := server.Connect(t.Context(), st, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ss.Close() })
			return ct
		},
		"streamable": func(t *testing.T, server *Server) Transport {
			hs := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, nil))
			t.Cleanup(hs.Close)
			return &StreamableClientTransport{Endpoint: hs.URL}
		},
		"streamable JSON response": func(t *testing.T, server *Server) Transport {
			hs := httptest.NewServer(NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{JSONResponse: true}))
			t.Cleanup(hs.Close)
			return &StreamableClientTransport{Endpoint: hs.URL}
		},
	}
	for name, transport := range transports {
		t.Run(name, func(t *testing.T) {
			elicited := make(chan *ElicitParams, 1)
			client := NewClient(testImpl, &ClientOptions{
				ElicitationHandler: func(_ context.Context, req *ElicitRequest) (*ElicitResult, error) {
					elicited <- req.Params
					return &ElicitResult{Action: "decline"}, nil
				},
			})
			// The server sends elicitation/create while serving tools/call, which
			// 2026-07-28 replaces with input requests (tested below).
			opts := &ClientSessionOptions{ProtocolVersion: protocolVersion20251125}
			cs, err := client.Connect(t.Context(), transport(t, newServer()), opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cs.Close() })

			tools, err := cs.ListTools(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			checkExact(t, "Tool.InputSchema", tools.Tools[0].InputSchema, schema)
			checkExact(t, "Tool.OutputSchema", tools.Tools[0].OutputSchema, schema)
			checkExact(t, "Tool.Meta", tools.Tools[0].Meta, meta)

			result, err := cs.CallTool(t.Context(), &CallToolParams{Name: "exact"})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("CallTool returned a tool error: %v", result.Content)
			}
			checkExact(t, "CallToolResult.Meta", result.Meta, meta)
			checkExact(t, "TextContent.Meta", result.Content[0].(*TextContent).Meta, meta)
			checkExact(t, "StructuredContent", result.StructuredContent, `{"n":`+n+`}`)
			select {
			case params := <-elicited:
				checkExact(t, "ElicitParams.RequestedSchema", params.RequestedSchema, schema)
				checkExact(t, "ElicitParams.Meta", params.Meta, meta)
			default:
				t.Fatal("the elicitation handler was not called")
			}
		})
	}
}

// TestSchemaAndMetaNumbersInInputRequests checks the numbers a client receives
// in the elicitation input requests of a multi round-trip tool call.
func TestSchemaAndMetaNumbersInInputRequests(t *testing.T) {
	const n = "9007199254740993"
	schema := numericSchema(n)
	for _, jsonResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("JSONResponse=%t", jsonResponse), func(t *testing.T) {
			server := NewServer(testImpl, nil)
			server.AddTool(&Tool{Name: "input", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *CallToolRequest) (*CallToolResult, error) {
				return &CallToolResult{
					InputRequests: InputRequestMap{"form": &ElicitParams{
						Mode:            "form",
						Message:         "m",
						RequestedSchema: json.RawMessage(schema),
						Meta:            Meta{"n": json.RawMessage(n)},
					}},
					RequestState: "state",
				}, nil
			})
			handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{Stateless: true, JSONResponse: jsonResponse})
			hs := httptest.NewServer(handler)
			t.Cleanup(hs.Close)
			client := NewClient(testImpl, &ClientOptions{MultiRoundTrip: &MultiRoundTripOptions{Disabled: true}})
			cs, err := client.Connect(t.Context(), &StreamableClientTransport{Endpoint: hs.URL}, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cs.Close() })

			result, err := cs.CallTool(t.Context(), &CallToolParams{Name: "input"})
			if err != nil {
				t.Fatal(err)
			}
			if !result.NeedsInput() {
				t.Fatalf("CallTool result does not need input: %+v", result)
			}
			params, ok := result.InputRequests["form"].(*ElicitParams)
			if !ok {
				t.Fatalf("input request is %T, want *ElicitParams", result.InputRequests["form"])
			}
			checkExact(t, "ElicitParams.RequestedSchema", params.RequestedSchema, schema)
			checkExact(t, "ElicitParams.Meta", params.Meta, `{"n":`+n+`}`)
		})
	}
}
