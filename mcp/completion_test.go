// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReference(t *testing.T) {
	tests := []struct {
		name    string
		in      mcp.Reference
		want    string
		wantErr bool
	}{
		{
			name: "PromptReference",
			in:   mcp.Reference{Type: "ref/prompt", Name: "my_prompt"},
			want: `{"type":"ref/prompt","name":"my_prompt"}`,
		},
		{
			name: "ResourceReference",
			in:   mcp.Reference{Type: "ref/resource", URI: "file:///path/to/resource.txt"},
			want: `{"type":"ref/resource","uri":"file:///path/to/resource.txt"}`,
		},
		{
			name: "PromptReference with empty name",
			in:   mcp.Reference{Type: "ref/prompt", Name: ""},
			want: `{"type":"ref/prompt"}`,
		},
		{
			name: "ResourceReference with empty URI",
			in:   mcp.Reference{Type: "ref/resource", URI: ""},
			want: `{"type":"ref/resource"}`,
		},
		{
			name:    "Unrecognized Type",
			in:      mcp.Reference{Type: "ref/unknown", Name: "something"},
			want:    `{"type":"ref/unknown","name":"something"}`,
			wantErr: true,
		},
		{
			name:    "Missing Type Field",
			in:      mcp.Reference{Name: "missing"},
			want:    `{"type":"","name":"missing"}`,
			wantErr: true,
		},
		{
			name:    "Invalid JSON Format",
			in:      mcp.Reference{},
			want:    `{"type":""}`,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Test Marshal
			got, err := json.Marshal(test.in)
			if err != nil {
				t.Fatalf("json.Marshal(%v) failed: %v", test.in, err)
			}
			if diff := cmp.Diff(test.want, string(got)); diff != "" {
				t.Errorf("json.Marshal(%v) mismatch (-want +got):\n%s", test.in, diff)
			}

			// Test Unmarshal
			var unmarshaled mcp.Reference
			err = json.Unmarshal([]byte(test.want), &unmarshaled)

			if test.wantErr {
				if err == nil {
					t.Fatalf("json.Unmarshal(%q) should have failed", test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal(%q) failed: %v", test.want, err)
			}
			if diff := cmp.Diff(test.in, unmarshaled); diff != "" {
				t.Errorf("json.Unmarshal(%q) mismatch (-want +got):\n%s", test.want, diff)
			}
		})
	}
}

func TestCompleteParams(t *testing.T) {
	// Test CompleteParams
	params := mcp.CompleteParams{
		Ref: &mcp.Reference{
			Type: "ref/prompt",
			Name: "my_prompt",
		},
		Argument: mcp.CompleteParamsArgument{
			Name:  "language",
			Value: "go",
		},
	}
	wantParamsJSON := `{"argument":{"name":"language","value":"go"},"ref":{"type":"ref/prompt","name":"my_prompt"}}`

	gotParamsJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(CompleteParams) failed: %v", err)
	}
	if diff := cmp.Diff(wantParamsJSON, string(gotParamsJSON)); diff != "" {
		t.Errorf("CompleteParams marshal mismatch (-want +got):\n%s", diff)
	}

	var unmarshaledParams mcp.CompleteParams
	if err := json.Unmarshal([]byte(wantParamsJSON), &unmarshaledParams); err != nil {
		t.Fatalf("json.Unmarshal(CompleteParams) failed: %v", err)
	}
	if diff := cmp.Diff(params, unmarshaledParams); diff != "" {
		t.Errorf("CompleteParams unmarshal mismatch (-want +got):\n%s", diff)
	}
}

func TestCompleteResult(t *testing.T) {
	res := mcp.CompleteResult{
		Completion: mcp.CompletionResultDetails{
			Values:  []string{"golang", "google", "goroutine"},
			Total:   10,
			HasMore: true,
		},
	}
	wantResJSON := `{"completion":{"hasMore":true,"total":10,"values":["golang","google","goroutine"]}}`
	gotResJSON, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(CompleteResult) failed: %v", err)
	}
	if diff := cmp.Diff(wantResJSON, string(gotResJSON)); diff != "" {
		t.Errorf("CompleteResult marshal mismatch (-want +got):\n%s", diff)
	}

	var unmarshaledRes mcp.CompleteResult
	if err := json.Unmarshal([]byte(wantResJSON), &unmarshaledRes); err != nil {
		t.Fatalf("json.Unmarshal(CompleteResult) failed: %v", err)
	}
	if diff := cmp.Diff(res, unmarshaledRes); diff != "" {
		t.Errorf("CompleteResult unmarshal mismatch (-want +got):\n%s", diff)
	}
}
