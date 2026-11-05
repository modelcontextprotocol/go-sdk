// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	internaljson "github.com/modelcontextprotocol/go-sdk/internal/json"
)

const elicitationNumericSchema = `{
	"type":"object",
	"properties":{
		"large":{"type":"integer","minimum":9007199254740993,"maximum":922337203685477580812345678901,"enum":[9007199254740993,922337203685477580812345678901],"default":9007199254740993},
		"decimal":{"type":"number","minimum":0.12345678901234567890123456789,"maximum":1.2300e+12,"enum":[0.12345678901234567890123456789,1.2300e+12],"default":0.12345678901234567890123456789}
	},
	"required":["large","decimal"]
}`

func TestElicitParamsJSONPreservesNumbers(t *testing.T) {
	for _, decoder := range []struct {
		name      string
		unmarshal func([]byte, any) error
	}{
		{"standard JSON", json.Unmarshal},
		{"SDK JSON", internaljson.Unmarshal},
	} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/input-map=%t", decoder.name, wrapped), func(t *testing.T) {
				data := `{"mode":"form","message":"Enter values","requestedSchema":` + elicitationNumericSchema + `,"_meta":{"cursor":9007199254740993,"amount":0.12345678901234567890123456789}}`
				params := new(ElicitParams)
				if wrapped {
					var requests InputRequestMap
					if err := decoder.unmarshal([]byte(`{"form":{"method":"elicitation/create","params":`+data+`}}`), &requests); err != nil {
						t.Fatal(err)
					}
					var ok bool
					params, ok = requests["form"].(*ElicitParams)
					if !ok {
						t.Fatalf("input request = %T, want *ElicitParams", requests["form"])
					}
				} else if err := decoder.unmarshal([]byte(data), params); err != nil {
					t.Fatal(err)
				}
				assertElicitationExactValue(t, params.RequestedSchema, elicitationNumericSchema)
				if params.Mode != "form" || params.Message != "Enter values" || params.Meta["cursor"] != json.Number("9007199254740993") || params.Meta["amount"] != json.Number("0.12345678901234567890123456789") {
					t.Fatalf("elicitation params = %#v", params)
				}
				encoded, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				var roundtrip ElicitParams
				if err := decoder.unmarshal(encoded, &roundtrip); err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(params, &roundtrip); diff != "" {
					t.Errorf("round trip changed elicitation params (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestElicitParamsJSONDecodeIsTransactional(t *testing.T) {
	params := &ElicitParams{Mode: "form", Message: "original", RequestedSchema: map[string]any{"default": json.Number("9007199254740993")}, Meta: Meta{"original": json.RawMessage("9007199254740993")}}
	before, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"requestedSchema":{"default":1},"_meta":{"changed":1},"message":false}`), params); err == nil {
		t.Fatal("invalid JSON field type was accepted")
	}
	after, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("failed decode changed receiver:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestMultiRoundTripHTTPElicitParamsPreserveNumbers(t *testing.T) {
	for _, jsonResponse := range []bool{false, true} {
		t.Run(fmt.Sprintf("JSON response=%t", jsonResponse), func(t *testing.T) {
			server := NewServer(testImpl, nil)
			server.AddTool(&Tool{Name: "numbers", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *CallToolRequest) (*CallToolResult, error) {
				return &CallToolResult{
					InputRequests: InputRequestMap{"form": &ElicitParams{
						Mode: "form", Message: "Enter values", RequestedSchema: json.RawMessage(elicitationNumericSchema),
						Meta: Meta{"cursor": json.RawMessage("9007199254740993")},
					}},
					RequestState: "form-state",
				}, nil
			})
			handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{Stateless: true, JSONResponse: jsonResponse})
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)
			client := NewClient(testImpl, &ClientOptions{MultiRoundTrip: &MultiRoundTripOptions{Disabled: true}})
			session, err := client.Connect(t.Context(), &StreamableClientTransport{Endpoint: httpServer.URL}, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { session.Close() })
			if version := session.InitializeResult().ProtocolVersion; version != protocolVersion20260728 {
				t.Fatalf("protocol version = %q, want %q", version, protocolVersion20260728)
			}
			response, err := session.CallTool(t.Context(), &CallToolParams{Name: "numbers"})
			if err != nil {
				t.Fatal(err)
			}
			if !response.NeedsInput() || response.RequestState != "form-state" {
				t.Fatalf("initial call did not require the expected form input: %#v", response)
			}
			params, ok := response.InputRequests["form"].(*ElicitParams)
			if !ok {
				t.Fatalf("input request = %T, want *ElicitParams", response.InputRequests["form"])
			}
			assertElicitationExactValue(t, params.RequestedSchema, elicitationNumericSchema)
			if params.Meta["cursor"] != json.Number("9007199254740993") {
				t.Fatalf("input request metadata = %v", params.Meta["cursor"])
			}
		})
	}
}

func assertElicitationExactValue(t *testing.T, value any, data string) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	var want any
	if err := decoder.Decode(&want); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, value); diff != "" {
		t.Errorf("elicitation JSON changed values or numeric types (-want +got):\n%s", diff)
	}
}
