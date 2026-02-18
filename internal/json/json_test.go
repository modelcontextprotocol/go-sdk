// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package json

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUnmarshalCaseSensitivity(t *testing.T) {
	type Nested struct {
		Field string `json:"field"`
	}
	type Target struct {
		Field       string
		TaggedField string `json:"custom_tag"`
		Nested      *Nested
	}

	tests := []struct {
		name string
		json string
		want Target
	}{
		{
			name: "exact match",
			json: `{"Field": "value", "custom_tag": "tagged", "Nested": {"field": "nested"}}`,
			want: Target{
				Field:       "value",
				TaggedField: "tagged",
				Nested: &Nested{
					Field: "nested",
				},
			},
		},
		{
			name: "case mismatch",
			json: `{"field": "value", "Custom_tag": "tagged", "Nested": {"Field": "nested"}}`,
			want: Target{
				Field:       "",
				TaggedField: "",
				Nested: &Nested{
					Field: "",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Target
			if err := Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Unmarshal mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
