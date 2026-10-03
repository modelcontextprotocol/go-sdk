// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestApplySchemaPreservesLargeIntegers covers integers that are exact in
// JSON and in int64 but NOT in float64 — anything above 2^53.
//
// applySchema unmarshals into `any` and re-marshals whenever defaults may
// have been applied, which is always the case for an object-rooted schema.
// Decoding a JSON number into `any` without UseNumber yields a float64, so
// the re-marshal wrote back a value that had already lost precision:
// 9007199254740993 came out as 9007199254740992, silently, with nothing
// reporting a problem.
//
// Snowflake ids, Discord/Twitter-style ids and any BIGINT primary key
// allocated above 2^53 are all in range, on both the argument and the
// result path.
func TestApplySchemaPreservesLargeIntegers(t *testing.T) {
	const big = int64(9007199254740993) // 2^53 + 1

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"id":    {Type: "integer"},
			"ratio": {Type: "number"},
		},
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	for _, forOutput := range []bool{true, false} {
		name := "output"
		if !forOutput {
			name = "input"
		}
		t.Run(name, func(t *testing.T) {
			in := json.RawMessage(`{"id":9007199254740993,"ratio":1.5}`)
			got, err := applySchema(in, resolved, forOutput)
			if err != nil {
				t.Fatalf("applySchema: %v", err)
			}

			var out struct {
				ID    int64   `json:"id"`
				Ratio float64 `json:"ratio"`
			}
			if err := json.Unmarshal(got, &out); err != nil {
				t.Fatalf("unmarshal result %s: %v", got, err)
			}
			if out.ID != big {
				t.Errorf("id = %d, want %d (lost %d)\nresult: %s", out.ID, big, big-out.ID, got)
			}
			if out.Ratio != 1.5 {
				t.Errorf("ratio = %v, want 1.5\nresult: %s", out.Ratio, got)
			}
		})
	}
}

// TestApplySchemaKeepsFloatsAsFloats pins the other half: narrowing to
// int64 must not turn a value the schema calls a number into an integer,
// and must not disturb a float that happens to have no fractional part
// beyond what JSON already says about it.
func TestApplySchemaKeepsFloatsAsFloats(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"ratio": {Type: "number"},
		},
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	got, err := applySchema(json.RawMessage(`{"ratio":0.1}`), resolved, true)
	if err != nil {
		t.Fatalf("applySchema: %v", err)
	}
	var out struct {
		Ratio float64 `json:"ratio"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Ratio != 0.1 {
		t.Errorf("ratio = %v, want 0.1\nresult: %s", out.Ratio, got)
	}
}

// TestApplySchemaNarrowsNestedIntegers pins that the narrowing reaches
// inside arrays and nested objects — a database row set is an array of
// arrays, which is exactly where these ids live.
func TestApplySchemaNarrowsNestedIntegers(t *testing.T) {
	schema := &jsonschema.Schema{Type: "object"}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	in := json.RawMessage(`{"rows":[[9007199254740993],[9007199254740995]],"meta":{"max":9007199254740997}}`)
	got, err := applySchema(in, resolved, true)
	if err != nil {
		t.Fatalf("applySchema: %v", err)
	}
	var out struct {
		Rows [][]int64 `json:"rows"`
		Meta struct {
			Max int64 `json:"max"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Rows[0][0] != 9007199254740993 || out.Rows[1][0] != 9007199254740995 {
		t.Errorf("rows = %v, want [[9007199254740993] [9007199254740995]]\nresult: %s", out.Rows, got)
	}
	if out.Meta.Max != 9007199254740997 {
		t.Errorf("meta.max = %d, want 9007199254740997\nresult: %s", out.Meta.Max, got)
	}
}
