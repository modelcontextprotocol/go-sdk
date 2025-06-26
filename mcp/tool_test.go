// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

// testToolHandler is used for type inference in TestNewServerTool.
func testToolHandler[T ToolInput](context.Context, *ServerSession, *CallToolParamsFor[json.RawMessage]) (T, error) {
	panic("not implemented")
}

type Basic struct {
	Name string `json:"name"`
}

func (b Basic) Result() (*CallToolResult, error) {
	return &CallToolResult{
		Content: []Content{&TextContent{Text: b.Name}},
	}, nil
}

func (b Basic) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[Basic]()
}

func (b Basic) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, &b)
}

type EnumExample struct{ Name string }

func (e EnumExample) Result() (*CallToolResult, error) {
	return &CallToolResult{
		Content: []Content{&TextContent{Text: e.Name}},
	}, nil
}

func (e EnumExample) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[EnumExample]()
}

func (e EnumExample) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, &e)
}

type RequiredExample struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	X        int    `json:"x,omitempty"`
	Y        int    `json:"y,omitempty"`
}

func (r RequiredExample) Result() (*CallToolResult, error) {
	return &CallToolResult{
		Content: []Content{
			&TextContent{Text: r.Name},
			&TextContent{Text: r.Language},
		},
	}, nil
}

func (r RequiredExample) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[RequiredExample]()
}

func (r RequiredExample) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, &r)
}

type testTool struct {
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
}

func (t testTool) Result() (*CallToolResult, error) {
	return &CallToolResult{
		Content: []Content{
			&TextContent{Text: fmt.Sprintf("X: %d", t.X)},
			&TextContent{Text: fmt.Sprintf("Y: %d", t.Y)},
		},
	}, nil
}

func (t testTool) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[testTool]()
}

func (t testTool) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, &t)
}

func TestNewServerTool(t *testing.T) {

	tests := []struct {
		tool *ServerTool
		want *jsonschema.Schema
	}{
		{
			NewServerTool[Basic]("basic", "", testToolHandler[Basic]),
			&jsonschema.Schema{
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
				AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
			},
		},
		{
			NewServerTool[EnumExample]("enum", "", testToolHandler[EnumExample], Input(
				Property("Name", Enum("x", "y", "z")),
			)),
			&jsonschema.Schema{
				Type:     "object",
				Required: []string{"Name"},
				Properties: map[string]*jsonschema.Schema{
					"Name": {Type: "string", Enum: []any{"x", "y", "z"}},
				},
				AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
			},
		},
		{
			NewServerTool[RequiredExample]("required", "", testToolHandler[RequiredExample], Input(
				Property("x", Required(true)))),
			&jsonschema.Schema{
				Type:     "object",
				Required: []string{"name", "language", "x"},
				Properties: map[string]*jsonschema.Schema{
					"language": {Type: "string"},
					"name":     {Type: "string"},
					"x":        {Type: "integer"},
					"y":        {Type: "integer"},
				},
				AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
			},
		},
		{
			NewServerTool[testTool]("set_schema", "", testToolHandler[testTool], Input(
				Schema(&jsonschema.Schema{Type: "object"})),
			),
			&jsonschema.Schema{
				Type: "object",
			},
		},
	}
	for _, test := range tests {
		if diff := cmp.Diff(test.want, test.tool.Tool.InputSchema, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("NewServerTool(%v) mismatch (-want +got):\n%s", test.tool.Tool.Name, diff)
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
