// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

// testToolHandler is used for type inference in TestNewServerTool.
func testToolHandler[In, Out any](context.Context, *ServerSession, *CallToolParamsFor[In]) (*CallToolResultFor[Out], error) {
	panic("not implemented")
}

func srvTool[In, Out any](t *testing.T, tool *Tool, handler ToolHandlerFor[In, Out]) *serverTool {
	t.Helper()
	st, err := newServerTool(tool, handler)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestNewServerTool(t *testing.T) {
	type (
		Name struct {
			Name string `json:"name"`
		}
		Size struct {
			Size int `json:"size"`
		}
	)

	nameSchema := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"name"},
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
		AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
	}
	sizeSchema := &jsonschema.Schema{
		Type:     "object",
		Required: []string{"size"},
		Properties: map[string]*jsonschema.Schema{
			"size": {Type: "integer"},
		},
		AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
	}

	tests := []struct {
		tool            *serverTool
		wantIn, wantOut *jsonschema.Schema
	}{
		{
			srvTool(t, &Tool{Name: "basic"}, testToolHandler[Name, Size]),
			nameSchema,
			sizeSchema,
		},
		{
			srvTool(t, &Tool{
				Name:        "in untouched",
				InputSchema: &jsonschema.Schema{},
			}, testToolHandler[Name, Size]),
			&jsonschema.Schema{},
			sizeSchema,
		},
		{
			srvTool(t, &Tool{Name: "out untouched", OutputSchema: &jsonschema.Schema{}}, testToolHandler[Name, Size]),
			nameSchema,
			&jsonschema.Schema{},
		},
		{
			srvTool(t, &Tool{Name: "nil out"}, testToolHandler[Name, any]),
			nameSchema,
			nil,
		},
	}
	for _, test := range tests {
		if diff := cmp.Diff(test.wantIn, test.tool.tool.InputSchema, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("newServerTool(%q) input schema mismatch (-want +got):\n%s", test.tool.tool.Name, diff)
		}
		if diff := cmp.Diff(test.wantOut, test.tool.tool.OutputSchema, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("newServerTool(%q) output schema mismatch (-want +got):\n%s", test.tool.tool.Name, diff)
		}
	}
}

func TestUnmarshalSchema(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"x": {Type: "integer", Default: json.RawMessage("3")},
		},
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatal(err)
	}

	type S struct {
		X int `json:"x"`
	}

	for _, tt := range []struct {
		data string
		v    any
		want any
	}{
		{`{"x": 1}`, new(S), &S{X: 1}},
		{`{}`, new(S), &S{X: 3}},       // default applied
		{`{"x": 0}`, new(S), &S{X: 3}}, // FAIL: should be 0. (requires double unmarshal)
		{`{"x": 1}`, new(map[string]any), &map[string]any{"x": 1.0}},
		{`{}`, new(map[string]any), &map[string]any{"x": 3.0}}, // default applied
		{`{"x": 0}`, new(map[string]any), &map[string]any{"x": 0.0}},
	} {
		raw := json.RawMessage(tt.data)
		if err := unmarshalSchema(raw, resolved, tt.v); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tt.v, tt.want) {
			t.Errorf("got %#v, want %#v", tt.v, tt.want)
		}

	}
}
