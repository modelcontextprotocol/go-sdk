// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonschema_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

func forType[T any]() *jsonschema.Schema {
	s, err := jsonschema.For[T]()
	if err != nil {
		panic(err)
	}
	return s
}

func TestForType(t *testing.T) {
	type schema = jsonschema.Schema
	tests := []struct {
		name string
		got  *jsonschema.Schema
		want *jsonschema.Schema
	}{
		{"string", forType[string](), &schema{Type: "string"}},
		{"int", forType[int](), &schema{Type: "integer"}},
		{"int16", forType[int16](), &schema{Type: "integer"}},
		{"uint32", forType[int16](), &schema{Type: "integer"}},
		{"float64", forType[float64](), &schema{Type: "number"}},
		{"bool", forType[bool](), &schema{Type: "boolean"}},
		{"intmap", forType[map[string]int](), &schema{
			Type:                 "object",
			AdditionalProperties: &schema{Type: "integer"},
		}},
		{"anymap", forType[map[string]any](), &schema{
			Type:                 "object",
			AdditionalProperties: &schema{},
		}},
		{
			"struct",
			forType[struct {
				F           int `json:"f"`
				G           []float64
				P           *bool
				Skip        string `json:"-"`
				NoSkip      string `json:",omitempty"`
				unexported  float64
				unexported2 int `json:"No"`
			}](),
			&schema{
				Type: "object",
				Properties: map[string]*schema{
					"f":      {Type: "integer"},
					"G":      {Type: "array", Items: &schema{Type: "number"}},
					"P":      {Types: []string{"null", "boolean"}},
					"NoSkip": {Type: "string"},
				},
				Required:             []string{"f", "G", "P"},
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
			},
		},
		{
			"no sharing",
			forType[struct{ X, Y int }](),
			&schema{
				Type: "object",
				Properties: map[string]*schema{
					"X": {Type: "integer"},
					"Y": {Type: "integer"},
				},
				Required:             []string{"X", "Y"},
				AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if diff := cmp.Diff(test.want, test.got, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("ForType mismatch (-want +got):\n%s", diff)
			}
			// These schemas should all resolve.
			if _, err := test.got.Resolve(nil); err != nil {
				t.Fatalf("Resolving: %v", err)
			}
		})
	}
}

func TestForWithMutation(t *testing.T) {
	// This test ensures that the cached schema is not mutated when the caller
	// mutates the returned schema.
	type S struct {
		A int
	}
	type T struct {
		A int `json:"A"`
		B map[string]int
		C []S
		D [3]S
		E *bool
	}
	s, err := jsonschema.For[T]()
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	s.Required[0] = "mutated"
	s.Properties["A"].Type = "mutated"
	s.Properties["C"].Items.Type = "mutated"
	s.Properties["D"].MaxItems = jsonschema.Ptr(10)
	s.Properties["D"].MinItems = jsonschema.Ptr(10)
	s.Properties["E"].Types[0] = "mutated"

	s2, err := jsonschema.For[T]()
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if s2.Properties["A"].Type == "mutated" {
		t.Fatalf("ForWithMutation: expected A.Type to not be mutated")
	}
	if s2.Properties["B"].AdditionalProperties.Type == "mutated" {
		t.Fatalf("ForWithMutation: expected B.AdditionalProperties.Type to not be mutated")
	}
	if s2.Properties["C"].Items.Type == "mutated" {
		t.Fatalf("ForWithMutation: expected C.Items.Type to not be mutated")
	}
	if *s2.Properties["D"].MaxItems == 10 {
		t.Fatalf("ForWithMutation: expected D.MaxItems to not be mutated")
	}
	if *s2.Properties["D"].MinItems == 10 {
		t.Fatalf("ForWithMutation: expected D.MinItems to not be mutated")
	}
	if s2.Properties["E"].Types[0] == "mutated" {
		t.Fatalf("ForWithMutation: expected E.Types[0] to not be mutated")
	}
	if s2.Required[0] == "mutated" {
		t.Fatalf("ForWithMutation: expected Required[0] to not be mutated")
	}
}

type x struct {
	Y y
}
type y struct {
	X []x
}

func TestForWithCycle(t *testing.T) {
	type a []*a
	type b1 struct{ b *b1 } // unexported field should be skipped
	type b2 struct{ B *b2 }
	type c1 struct{ c map[string]*c1 } // unexported field should be skipped
	type c2 struct{ C map[string]*c2 }

	tests := []struct {
		name      string
		shouldErr bool
		fn        func() error
	}{
		{"slice alias (a)", true, func() error { _, err := jsonschema.For[a](); return err }},
		{"unexported self cycle (b1)", false, func() error { _, err := jsonschema.For[b1](); return err }},
		{"exported self cycle (b2)", true, func() error { _, err := jsonschema.For[b2](); return err }},
		{"unexported map self cycle (c1)", false, func() error { _, err := jsonschema.For[c1](); return err }},
		{"exported map self cycle (c2)", true, func() error { _, err := jsonschema.For[c2](); return err }},
		{"cross-cycle x -> y -> x", true, func() error { _, err := jsonschema.For[x](); return err }},
		{"cross-cycle y -> x -> y", true, func() error { _, err := jsonschema.For[y](); return err }},
	}

	for _, test := range tests {
		test := test // prevent loop shadowing
		t.Run(test.name, func(t *testing.T) {
			err := test.fn()
			if test.shouldErr && err == nil {
				t.Errorf("expected cycle error, got nil")
			}
			if !test.shouldErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
