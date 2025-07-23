// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonschema_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

func forType[T any](lax bool) *jsonschema.Schema {
	var s *jsonschema.Schema
	var err error
	if lax {
		s, err = jsonschema.ForLax[T]()
	} else {
		s, err = jsonschema.For[T]()
	}
	if err != nil {
		panic(err)
	}
	return s
}

func TestFor(t *testing.T) {
	type schema = jsonschema.Schema

	type S struct {
		B int `jsonschema:"bdesc"`
	}

	type test struct {
		name string
		got  *jsonschema.Schema
		want *jsonschema.Schema
	}

	tests := func(lax bool) []test {
		return []test{
			{"string", forType[string](lax), &schema{Type: "string"}},
			{"int", forType[int](lax), &schema{Type: "integer"}},
			{"int16", forType[int16](lax), &schema{Type: "integer"}},
			{"uint32", forType[int16](lax), &schema{Type: "integer"}},
			{"float64", forType[float64](lax), &schema{Type: "number"}},
			{"bool", forType[bool](lax), &schema{Type: "boolean"}},
			{"intmap", forType[map[string]int](lax), &schema{
				Type:                 "object",
				AdditionalProperties: &schema{Type: "integer"},
			}},
			{"anymap", forType[map[string]any](lax), &schema{
				Type:                 "object",
				AdditionalProperties: &schema{},
			}},
			{
				"struct",
				forType[struct {
					F           int `json:"f" jsonschema:"fdesc"`
					G           []float64
					P           *bool  `jsonschema:"pdesc"`
					Skip        string `json:"-"`
					NoSkip      string `json:",omitempty"`
					unexported  float64
					unexported2 int `json:"No"`
				}](lax),
				&schema{
					Type: "object",
					Properties: map[string]*schema{
						"f":      {Type: "integer", Description: "fdesc"},
						"G":      {Type: "array", Items: &schema{Type: "number"}},
						"P":      {Types: []string{"null", "boolean"}, Description: "pdesc"},
						"NoSkip": {Type: "string"},
					},
					Required:             []string{"f", "G", "P"},
					AdditionalProperties: falseSchema(),
				},
			},
			{
				"no sharing",
				forType[struct{ X, Y int }](lax),
				&schema{
					Type: "object",
					Properties: map[string]*schema{
						"X": {Type: "integer"},
						"Y": {Type: "integer"},
					},
					Required:             []string{"X", "Y"},
					AdditionalProperties: falseSchema(),
				},
			},
			{
				"nested and embedded",
				forType[struct {
					A S
					S
				}](lax),
				&schema{
					Type: "object",
					Properties: map[string]*schema{
						"A": {
							Type: "object",
							Properties: map[string]*schema{
								"B": {Type: "integer", Description: "bdesc"},
							},
							Required:             []string{"B"},
							AdditionalProperties: falseSchema(),
						},
						"S": {
							Type: "object",
							Properties: map[string]*schema{
								"B": {Type: "integer", Description: "bdesc"},
							},
							Required:             []string{"B"},
							AdditionalProperties: falseSchema(),
						},
					},
					Required:             []string{"A", "S"},
					AdditionalProperties: falseSchema(),
				},
			},
		}
	}

	run := func(t *testing.T, tt test) {
		if diff := cmp.Diff(tt.want, tt.got, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Fatalf("ForType mismatch (-want +got):\n%s", diff)
		}
		// These schemas should all resolve.
		if _, err := tt.got.Resolve(nil); err != nil {
			t.Fatalf("Resolving: %v", err)
		}
	}

	t.Run("strict", func(t *testing.T) {
		for _, test := range tests(false) {
			t.Run(test.name, func(t *testing.T) { run(t, test) })
		}
	})

	laxTests := append(tests(true), test{
		"ignore",
		forType[struct {
			A int
			B map[int]int
			C func()
		}](true),
		&schema{
			Type: "object",
			Properties: map[string]*schema{
				"A": {Type: "integer"},
			},
			Required:             []string{"A"},
			AdditionalProperties: falseSchema(),
		},
	})
	t.Run("lax", func(t *testing.T) {
		for _, test := range laxTests {
			t.Run(test.name, func(t *testing.T) { run(t, test) })
		}
	})
}

func forErr[T any]() error {
	_, err := jsonschema.For[T]()
	return err
}

func TestForErrors(t *testing.T) {
	type (
		s1 struct {
			Empty int `jsonschema:""`
		}
		s2 struct {
			Bad int `jsonschema:"$foo=1,bar"`
		}
	)

	for _, tt := range []struct {
		got  error
		want string
	}{
		{forErr[map[int]int](), "unsupported map key type"},
		{forErr[s1](), "empty jsonschema tag"},
		{forErr[s2](), "must not begin with"},
		{forErr[func()](), "unsupported"},
	} {
		if tt.got == nil {
			t.Errorf("got nil, want error containing %q", tt.want)
		} else if !strings.Contains(tt.got.Error(), tt.want) {
			t.Errorf("got %q\nwant it to contain %q", tt.got, tt.want)
		}
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

func falseSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Not: &jsonschema.Schema{}}
}
