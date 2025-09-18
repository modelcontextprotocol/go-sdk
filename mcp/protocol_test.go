// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParamsMeta(t *testing.T) {
	// Verify some properties of the Meta field of Params structs.
	// We use CallToolParams for the test, but the Meta setup of all params types
	// is identical so they should all behave the same.

	toJSON := func(x any) string {
		data, err := json.Marshal(x)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	meta := map[string]any{"m": 1}

	// You can set the embedded Meta field to a literal map.
	p := &CallToolParams{
		Meta: meta,
		Name: "name",
	}

	// The Meta field marshals properly when it's present.
	if g, w := toJSON(p), `{"_meta":{"m":1},"name":"name"}`; g != w {
		t.Errorf("got %s, want %s", g, w)
	}
	// ... and when it's absent.
	p2 := &CallToolParams{Name: "n"}
	if g, w := toJSON(p2), `{"name":"n"}`; g != w {
		t.Errorf("got %s, want %s", g, w)
	}

	// The GetMeta and SetMeta functions work as expected.
	if g := p.GetMeta(); !maps.Equal(g, meta) {
		t.Errorf("got %+v, want %+v", g, meta)
	}

	meta2 := map[string]any{"x": 2}
	p.SetMeta(meta2)
	if g := p.GetMeta(); !maps.Equal(g, meta2) {
		t.Errorf("got %+v, want %+v", g, meta2)
	}

	// The GetProgressToken and SetProgressToken methods work as expected.
	if g := p.GetProgressToken(); g != nil {
		t.Errorf("got %v, want nil", g)
	}

	p.SetProgressToken("t")
	if g := p.GetProgressToken(); g != "t" {
		t.Errorf("got %v, want `t`", g)
	}

	// You can set a progress token to an int, int32 or int64.
	p.SetProgressToken(int(1))
	p.SetProgressToken(int32(1))
	p.SetProgressToken(int64(1))
}

func TestCompleteReference(t *testing.T) {
	marshalTests := []struct {
		name    string
		in      CompleteReference // The Go struct to marshal
		want    string            // The expected JSON string output
		wantErr bool              // True if json.Marshal is expected to return an error
	}{
		{
			name:    "ValidPrompt",
			in:      CompleteReference{Type: "ref/prompt", Name: "my_prompt"},
			want:    `{"type":"ref/prompt","name":"my_prompt"}`,
			wantErr: false,
		},
		{
			name:    "ValidResource",
			in:      CompleteReference{Type: "ref/resource", URI: "file:///path/to/resource.txt"},
			want:    `{"type":"ref/resource","uri":"file:///path/to/resource.txt"}`,
			wantErr: false,
		},
		{
			name:    "ValidPromptEmptyName",
			in:      CompleteReference{Type: "ref/prompt", Name: ""},
			want:    `{"type":"ref/prompt"}`,
			wantErr: false,
		},
		{
			name:    "ValidResourceEmptyURI",
			in:      CompleteReference{Type: "ref/resource", URI: ""},
			want:    `{"type":"ref/resource"}`,
			wantErr: false,
		},
		// Error cases for MarshalJSON
		{
			name:    "InvalidType",
			in:      CompleteReference{Type: "ref/unknown", Name: "something"},
			wantErr: true,
		},
		{
			name:    "PromptWithURI",
			in:      CompleteReference{Type: "ref/prompt", Name: "my_prompt", URI: "unexpected_uri"},
			wantErr: true,
		},
		{
			name:    "ResourceWithName",
			in:      CompleteReference{Type: "ref/resource", URI: "my_uri", Name: "unexpected_name"},
			wantErr: true,
		},
		{
			name:    "MissingTypeField",
			in:      CompleteReference{Name: "missing"}, // Type is ""
			wantErr: true,
		},
	}

	// Define test cases specifically for Unmarshalling
	unmarshalTests := []struct {
		name    string
		in      string            // The JSON string input
		want    CompleteReference // The expected Go struct output
		wantErr bool              // True if json.Unmarshal is expected to return an error
	}{
		{
			name:    "ValidPrompt",
			in:      `{"type":"ref/prompt","name":"my_prompt"}`,
			want:    CompleteReference{Type: "ref/prompt", Name: "my_prompt"},
			wantErr: false,
		},
		{
			name:    "ValidResource",
			in:      `{"type":"ref/resource","uri":"file:///path/to/resource.txt"}`,
			want:    CompleteReference{Type: "ref/resource", URI: "file:///path/to/resource.txt"},
			wantErr: false,
		},
		// Error cases for UnmarshalJSON
		{
			name:    "UnrecognizedType",
			in:      `{"type":"ref/unknown","name":"something"}`,
			want:    CompleteReference{}, // placeholder, as unmarshal will fail
			wantErr: true,
		},
		{
			name:    "PromptWithURI",
			in:      `{"type":"ref/prompt","name":"my_prompt","uri":"unexpected_uri"}`,
			want:    CompleteReference{}, // placeholder
			wantErr: true,
		},
		{
			name:    "ResourceWithName",
			in:      `{"type":"ref/resource","uri":"my_uri","name":"unexpected_name"}`,
			want:    CompleteReference{}, // placeholder
			wantErr: true,
		},
		{
			name:    "MissingType",
			in:      `{"name":"missing"}`,
			want:    CompleteReference{}, // placeholder
			wantErr: true,
		},
		{
			name:    "InvalidJSON",
			in:      `invalid json`,
			want:    CompleteReference{}, // placeholder
			wantErr: true,                // json.Unmarshal will fail natively
		},
	}

	// Run Marshal Tests
	for _, test := range marshalTests {
		t.Run("Marshal/"+test.name, func(t *testing.T) {
			gotBytes, err := json.Marshal(&test.in)
			if (err != nil) != test.wantErr {
				t.Errorf("json.Marshal(%v) got error %v (want error %t)", test.in, err, test.wantErr)
			}
			if !test.wantErr { // Only check JSON output if marshal was expected to succeed
				if diff := cmp.Diff(test.want, string(gotBytes)); diff != "" {
					t.Errorf("json.Marshal(%v) mismatch (-want +got):\n%s", test.in, diff)
				}
			}
		})
	}

	// Run Unmarshal Tests
	for _, test := range unmarshalTests {
		t.Run("Unmarshal/"+test.name, func(t *testing.T) {
			var got CompleteReference
			err := json.Unmarshal([]byte(test.in), &got)

			if (err != nil) != test.wantErr {
				t.Errorf("json.Unmarshal(%q) got error %v (want error %t)", test.in, err, test.wantErr)
			}
			if !test.wantErr { // Only check content if unmarshal was expected to succeed
				if diff := cmp.Diff(test.want, got); diff != "" {
					t.Errorf("json.Unmarshal(%q) mismatch (-want +got):\n%s", test.in, diff)
				}
			}
		})
	}
}

func TestCompleteParams(t *testing.T) {
	// Define test cases specifically for Marshalling
	marshalTests := []struct {
		name string
		in   CompleteParams
		want string // Expected JSON output
	}{
		{
			name: "BasicPromptCompletion",
			in: CompleteParams{
				Ref: &CompleteReference{
					Type: "ref/prompt",
					Name: "my_prompt",
				},
				Argument: CompleteParamsArgument{
					Name:  "language",
					Value: "go",
				},
			},
			want: `{"argument":{"name":"language","value":"go"},"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
		},
		{
			name: "ResourceCompletionRequest",
			in: CompleteParams{
				Ref: &CompleteReference{
					Type: "ref/resource",
					URI:  "file:///src/main.java",
				},
				Argument: CompleteParamsArgument{
					Name:  "class",
					Value: "MyClas",
				},
			},
			want: `{"argument":{"name":"class","value":"MyClas"},"ref":{"type":"ref/resource","uri":"file:///src/main.java"}}`,
		},
		{
			name: "PromptCompletionEmptyArgumentValue",
			in: CompleteParams{
				Ref: &CompleteReference{
					Type: "ref/prompt",
					Name: "another_prompt",
				},
				Argument: CompleteParamsArgument{
					Name:  "query",
					Value: "",
				},
			},
			want: `{"argument":{"name":"query","value":""},"ref":{"type":"ref/prompt","name":"another_prompt"}}`,
		},
		{
			name: "PromptCompletionWithContext",
			in: CompleteParams{
				Ref: &CompleteReference{
					Type: "ref/prompt",
					Name: "my_prompt",
				},
				Argument: CompleteParamsArgument{
					Name:  "language",
					Value: "go",
				},
				Context: &CompleteContext{
					Arguments: map[string]string{
						"framework": "mcp",
						"language":  "python",
					},
				},
			},
			want: `{"argument":{"name":"language","value":"go"},"context":{"arguments":{"framework":"mcp","language":"python"}},"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
		},
		{
			name: "PromptCompletionEmptyContextArguments",
			in: CompleteParams{
				Ref: &CompleteReference{
					Type: "ref/prompt",
					Name: "my_prompt",
				},
				Argument: CompleteParamsArgument{
					Name:  "language",
					Value: "go",
				},
				Context: &CompleteContext{
					Arguments: map[string]string{},
				},
			},
			want: `{"argument":{"name":"language","value":"go"},"context":{},"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
		},
	}

	// Define test cases specifically for Unmarshalling
	unmarshalTests := []struct {
		name string
		in   string         // JSON string input
		want CompleteParams // Expected Go struct output
	}{
		{
			name: "BasicPromptCompletion",
			in:   `{"argument":{"name":"language","value":"go"},"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
			want: CompleteParams{
				Ref:      &CompleteReference{Type: "ref/prompt", Name: "my_prompt"},
				Argument: CompleteParamsArgument{Name: "language", Value: "go"},
			},
		},
		{
			name: "ResourceCompletionRequest",
			in:   `{"argument":{"name":"class","value":"MyClas"},"ref":{"type":"ref/resource","uri":"file:///src/main.java"}}`,
			want: CompleteParams{
				Ref:      &CompleteReference{Type: "ref/resource", URI: "file:///src/main.java"},
				Argument: CompleteParamsArgument{Name: "class", Value: "MyClas"},
			},
		},
		{
			name: "PromptCompletionWithContext",
			in:   `{"argument":{"name":"language","value":"go"},"context":{"arguments":{"framework":"mcp","language":"python"}},"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
			want: CompleteParams{
				Ref:      &CompleteReference{Type: "ref/prompt", Name: "my_prompt"},
				Argument: CompleteParamsArgument{Name: "language", Value: "go"},
				Context: &CompleteContext{Arguments: map[string]string{
					"framework": "mcp",
					"language":  "python",
				}},
			},
		},
		{
			name: "PromptCompletionEmptyContextArguments",
			in:   `{"argument":{"name":"language","value":"go"},"context":{"arguments":{}},"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
			want: CompleteParams{
				Ref:      &CompleteReference{Type: "ref/prompt", Name: "my_prompt"},
				Argument: CompleteParamsArgument{Name: "language", Value: "go"},
				Context:  &CompleteContext{Arguments: map[string]string{}},
			},
		},
		{
			name: "PromptCompletionNilContext", // JSON `null` for context
			in:   `{"argument":{"name":"language","value":"go"},"context":null,"ref":{"type":"ref/prompt","name":"my_prompt"}}`,
			want: CompleteParams{
				Ref:      &CompleteReference{Type: "ref/prompt", Name: "my_prompt"},
				Argument: CompleteParamsArgument{Name: "language", Value: "go"},
				Context:  nil, // Should unmarshal to nil pointer
			},
		},
	}

	// Run Marshal Tests
	for _, test := range marshalTests {
		t.Run("Marshal/"+test.name, func(t *testing.T) {
			got, err := json.Marshal(&test.in) // Marshal takes a pointer
			if err != nil {
				t.Fatalf("json.Marshal(CompleteParams) failed: %v", err)
			}
			if diff := cmp.Diff(test.want, string(got)); diff != "" {
				t.Errorf("CompleteParams marshal mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Run Unmarshal Tests
	for _, test := range unmarshalTests {
		t.Run("Unmarshal/"+test.name, func(t *testing.T) {
			var got CompleteParams
			if err := json.Unmarshal([]byte(test.in), &got); err != nil {
				t.Fatalf("json.Unmarshal(CompleteParams) failed: %v", err)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("CompleteParams unmarshal mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCompleteResult(t *testing.T) {
	// Define test cases specifically for Marshalling
	marshalTests := []struct {
		name string
		in   CompleteResult
		want string // Expected JSON output
	}{
		{
			name: "BasicCompletionResult",
			in: CompleteResult{
				Completion: CompletionResultDetails{
					Values:  []string{"golang", "google", "goroutine"},
					Total:   10,
					HasMore: true,
				},
			},
			want: `{"completion":{"hasMore":true,"total":10,"values":["golang","google","goroutine"]}}`,
		},
		{
			name: "CompletionResultNoTotalNoHasMore",
			in: CompleteResult{
				Completion: CompletionResultDetails{
					Values:  []string{"only"},
					HasMore: false,
					Total:   0,
				},
			},
			want: `{"completion":{"values":["only"]}}`,
		},
		{
			name: "CompletionResultEmptyValues",
			in: CompleteResult{
				Completion: CompletionResultDetails{
					Values:  []string{},
					Total:   0,
					HasMore: false,
				},
			},
			want: `{"completion":{"values":[]}}`,
		},
	}

	// Define test cases specifically for Unmarshalling
	unmarshalTests := []struct {
		name string
		in   string         // JSON string input
		want CompleteResult // Expected Go struct output
	}{
		{
			name: "BasicCompletionResult",
			in:   `{"completion":{"hasMore":true,"total":10,"values":["golang","google","goroutine"]}}`,
			want: CompleteResult{
				Completion: CompletionResultDetails{
					Values:  []string{"golang", "google", "goroutine"},
					Total:   10,
					HasMore: true,
				},
			},
		},
		{
			name: "CompletionResultNoTotalNoHasMore",
			in:   `{"completion":{"values":["only"]}}`,
			want: CompleteResult{
				Completion: CompletionResultDetails{
					Values:  []string{"only"},
					HasMore: false,
					Total:   0,
				},
			},
		},
		{
			name: "CompletionResultEmptyValues",
			in:   `{"completion":{"hasMore":false,"total":0,"values":[]}}`,
			want: CompleteResult{
				Completion: CompletionResultDetails{
					Values:  []string{},
					Total:   0,
					HasMore: false,
				},
			},
		},
	}

	// Run Marshal Tests
	for _, test := range marshalTests {
		t.Run("Marshal/"+test.name, func(t *testing.T) {
			got, err := json.Marshal(&test.in) // Marshal takes a pointer
			if err != nil {
				t.Fatalf("json.Marshal(CompleteResult) failed: %v", err)
			}
			if diff := cmp.Diff(test.want, string(got)); diff != "" {
				t.Errorf("CompleteResult marshal mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Run Unmarshal Tests
	for _, test := range unmarshalTests {
		t.Run("Unmarshal/"+test.name, func(t *testing.T) {
			var got CompleteResult
			if err := json.Unmarshal([]byte(test.in), &got); err != nil {
				t.Fatalf("json.Unmarshal(CompleteResult) failed: %v", err)
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("CompleteResult unmarshal mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestContentUnmarshal(t *testing.T) {
	// Verify that types with a Content field round-trip properly.
	roundtrip := func(in, out any) {
		t.Helper()
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(in, out, ctrCmpOpts...); diff != "" {
			t.Errorf("mismatch (-want, +got):\n%s", diff)
		}
	}

	content := []Content{&TextContent{Text: "t"}}

	ctr := &CallToolResult{
		Meta:              Meta{"m": true},
		Content:           content,
		IsError:           true,
		StructuredContent: map[string]any{"s": "x"},
	}
	var got CallToolResult
	roundtrip(ctr, &got)

	ctrf := &CallToolResult{
		Meta:              Meta{"m": true},
		Content:           content,
		IsError:           true,
		StructuredContent: 3.0,
	}
	var gotf CallToolResult
	roundtrip(ctrf, &gotf)

	pm := &PromptMessage{
		Content: content[0],
		Role:    "",
	}
	var gotpm PromptMessage
	roundtrip(pm, &gotpm)
}
