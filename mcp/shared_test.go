// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TODO(jba): this shouldn't be in this file, but tool_test.go doesn't have access to unexported symbols.
func TestToolValidate(t *testing.T) {
	// Check that the tool returned from NewServerTool properly validates its input schema.

	type req struct {
		I int
		B bool
		S string `json:",omitempty"`
		P *int   `json:",omitempty"`
	}

	dummyHandler := func(context.Context, *ServerSession, *CallToolParamsFor[req]) (*CallToolResultFor[any], error) {
		return nil, nil
	}

	st, err := newServerTool(&Tool{Name: "test", Description: "test"}, dummyHandler)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		desc string
		args map[string]any
		want string // error should contain this string; empty for success
	}{
		{
			"both required",
			map[string]any{"I": 1, "B": true},
			"",
		},
		{
			"optional",
			map[string]any{"I": 1, "B": true, "S": "foo"},
			"",
		},
		{
			"wrong type",
			map[string]any{"I": 1.5, "B": true},
			"cannot unmarshal",
		},
		{
			"extra property",
			map[string]any{"I": 1, "B": true, "C": 2},
			"unknown field",
		},
		{
			"value for pointer",
			map[string]any{"I": 1, "B": true, "P": 3},
			"",
		},
		{
			"null for pointer",
			map[string]any{"I": 1, "B": true, "P": nil},
			"",
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			raw, err := json.Marshal(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.handler(context.Background(), nil,
				&CallToolParamsFor[json.RawMessage]{Arguments: json.RawMessage(raw)})
			if err == nil && tt.want != "" {
				t.Error("got success, wanted failure")
			}
			if err != nil {
				if tt.want == "" {
					t.Fatalf("failed with:\n%s\nwanted success", err)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("got:\n%s\nwanted to contain %q", err, tt.want)
				}
			}
		})
	}
}
