// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestMakeID(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    any // nil, int64 or string
		wantErr bool
	}{
		{name: "nil", in: nil},
		{name: "string", in: "abc", want: "abc"},
		{name: "float64 integer", in: float64(42), want: int64(42)},
		{name: "float64 negative integer", in: float64(-42), want: int64(-42)},
		{name: "float64 fraction", in: 1.5, wantErr: true},
		{name: "float64 above range", in: 1e30, wantErr: true},
		{name: "float64 below range", in: -1e30, wantErr: true},
		{name: "float64 NaN", in: math.NaN(), wantErr: true},
		{name: "float64 infinity", in: math.Inf(1), wantErr: true},
		{name: "json.Number integer", in: json.Number("42"), want: int64(42)},
		{name: "json.Number exponent", in: json.Number("1e3"), want: int64(1000)},
		{name: "json.Number fraction", in: json.Number("1.5"), wantErr: true},
		{name: "json.Number above range", in: json.Number("1e30"), wantErr: true},
		{name: "json.Number invalid", in: json.Number("abc"), wantErr: true},
		{name: "bool", in: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := MakeID(tt.in)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("MakeID(%v) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrParse) {
					t.Fatalf("MakeID(%v) error = %v, want a parse error", tt.in, err)
				}
				return
			}
			switch want := tt.want.(type) {
			case nil:
				if id != (ID{}) {
					t.Fatalf("MakeID(%v) = %v, want the zero ID", tt.in, id)
				}
			case int64:
				if id != Int64ID(want) {
					t.Fatalf("MakeID(%v) = %v, want %v", tt.in, id, Int64ID(want))
				}
			case string:
				if id != StringID(want) {
					t.Fatalf("MakeID(%v) = %v, want %v", tt.in, id, StringID(want))
				}
			}
		})
	}
}

// A fractional ID used to be truncated to the integer part, so a response
// carried an ID the client never sent; an out-of-range one became a random
// int64. Both have to fail the message instead.
func TestDecodeMessageRejectsNonIntegerID(t *testing.T) {
	for _, data := range []string{
		`{"jsonrpc":"2.0","id":1.5,"method":"m"}`,
		`{"jsonrpc":"2.0","id":1e30,"method":"m"}`,
	} {
		if _, err := DecodeMessage([]byte(data)); !errors.Is(err, ErrParse) {
			t.Errorf("DecodeMessage(%s) error = %v, want a parse error", data, err)
		}
	}
}

func TestDecodeMessageKeepsIntegerID(t *testing.T) {
	msg, err := DecodeMessage([]byte(`{"jsonrpc":"2.0","id":42,"method":"m"}`))
	if err != nil {
		t.Fatalf("DecodeMessage: %v", err)
	}
	req, ok := msg.(*Request)
	if !ok {
		t.Fatalf("DecodeMessage returned %T, want *Request", msg)
	}
	if req.ID != Int64ID(42) {
		t.Fatalf("ID = %v, want %v", req.ID, Int64ID(42))
	}
}
