// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

type SayHiParams struct {
	Name string `json:"name"`
}

func (p *SayHiParams) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[SayHiParams]()
}

func (p *SayHiParams) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, p)
}

type SayHiResult struct {
	Message string
}

func (r *SayHiResult) Result() (*CallToolResult, error) {
	return &CallToolResult{
		Content: []Content{
			&TextContent{Text: r.Message},
		},
	}, nil
}

func SayHi(ctx context.Context, cc *ServerSession, params *CallToolParamsFor[json.RawMessage]) (*SayHiResult, error) {
	var args SayHiParams
	if params.Arguments != nil {
		if err := args.SetParams(params.Arguments); err != nil {
			return nil, err
		}
	}

	return &SayHiResult{
		Message: "Hi " + args.Name,
	}, nil
}

func TestFeatureSetOrder(t *testing.T) {
	toolA := NewServerTool[*SayHiParams, *SayHiResult]("apple", "apple tool", SayHi).Tool
	toolB := NewServerTool[*SayHiParams, *SayHiResult]("banana", "banana tool", SayHi).Tool
	toolC := NewServerTool[*SayHiParams, *SayHiResult]("cherry", "cherry tool", SayHi).Tool

	testCases := []struct {
		tools []*Tool
		want  []*Tool
	}{
		{[]*Tool{toolA, toolB, toolC}, []*Tool{toolA, toolB, toolC}},
		{[]*Tool{toolB, toolC, toolA}, []*Tool{toolA, toolB, toolC}},
		{[]*Tool{toolA, toolC}, []*Tool{toolA, toolC}},
		{[]*Tool{toolA, toolA, toolA}, []*Tool{toolA}},
		{[]*Tool{}, nil},
	}
	for _, tc := range testCases {
		fs := newFeatureSet(func(t *Tool) string { return t.Name })
		fs.add(tc.tools...)
		got := slices.Collect(fs.all())
		if diff := cmp.Diff(got, tc.want, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("expected %v, got %v, (-want +got):\n%s", tc.want, got, diff)
		}
	}
}

func TestFeatureSetAbove(t *testing.T) {
	toolA := NewServerTool[*SayHiParams, *SayHiResult]("apple", "apple tool", SayHi).Tool
	toolB := NewServerTool[*SayHiParams, *SayHiResult]("banana", "banana tool", SayHi).Tool
	toolC := NewServerTool[*SayHiParams, *SayHiResult]("cherry", "cherry tool", SayHi).Tool

	testCases := []struct {
		tools []*Tool
		above string
		want  []*Tool
	}{
		{[]*Tool{toolA, toolB, toolC}, "apple", []*Tool{toolB, toolC}},
		{[]*Tool{toolA, toolB, toolC}, "banana", []*Tool{toolC}},
		{[]*Tool{toolA, toolB, toolC}, "cherry", nil},
	}
	for _, tc := range testCases {
		fs := newFeatureSet(func(t *Tool) string { return t.Name })
		fs.add(tc.tools...)
		got := slices.Collect(fs.above(tc.above))
		if diff := cmp.Diff(got, tc.want, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("expected %v, got %v, (-want +got):\n%s", tc.want, got, diff)
		}
	}
}
