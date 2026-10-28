// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeDecimalIntegerIDs(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want int64
	}{
		{"9007199254740993", 9007199254740993},
		{"9007199254740993.0", 9007199254740993},
		{"90071992547409930e-1", 9007199254740993},
		{"-9007199254740993.0", -9007199254740993},
		{"9223372036854775807.0", 9223372036854775807},
		{"-9223372036854775808.0", -9223372036854775808},
		{"9.007199254740993e15", 9007199254740993},
		{"9007199254740993000E-3", 9007199254740993},
		{"0.00042e5", 42},
		{"-0e999999999999999999999", 0},
		{"42.0", 42},
		{"420e-1", 42},
	} {
		t.Run(tc.wire, func(t *testing.T) {
			for _, suffix := range []string{`,"method":"m"}`, `,"result":{}}`} {
				wire := []byte(`{"jsonrpc":"2.0","id":` + tc.wire + suffix)
				msg, err := DecodeMessage(wire)
				if err != nil {
					t.Fatal(err)
				}
				var id ID
				switch m := msg.(type) {
				case *Request:
					id = m.ID
				case *Response:
					id = m.ID
				}
				if id != Int64ID(tc.want) {
					t.Errorf("wire %s: got %v, want %d", wire, id.Raw(), tc.want)
				}
			}
			id, err := MakeID(json.Number(tc.wire))
			if err != nil || id != Int64ID(tc.want) {
				t.Errorf("MakeID: got %v, err %v, want %d", id.Raw(), err, tc.want)
			}
		})
	}
}

func TestNumericIDLegacyFallback(t *testing.T) {
	for _, value := range []string{"1.5", "-1.5", "9223372036854775808", "-9223372036854775809", "1e100", "1e-100", "1e99999999999999999999", "invalid"} {
		f, oldErr := strconv.ParseFloat(value, 64)
		got, err := MakeID(json.Number(value))
		if oldErr != nil {
			if err == nil {
				t.Errorf("MakeID(%q) unexpectedly succeeded", value)
			}
		} else if err != nil || got != Int64ID(int64(f)) {
			t.Errorf("MakeID(%q) = %v, %v; want legacy %v", value, got.Raw(), err, int64(f))
		}
	}
	for _, f := range []float64{1.5, -1.5, 9007199254740992} {
		got, err := MakeID(f)
		if err != nil || got != Int64ID(int64(f)) {
			t.Errorf("MakeID(%v) = %v, %v", f, got.Raw(), err)
		}
	}
}

func FuzzExactIntegerID(f *testing.F) {
	for _, s := range []string{"9007199254740993.0", "90071992547409930e-1", "9e18", "-9223372036854775808.0", "1.5", "0", "0e99999999999999999999", "1e-9999999999999999999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 256 || !json.Valid([]byte(s)) || strings.TrimSpace(s) != s {
			return
		}
		// Bound the oracle's exponent; the production parser has no such input cap.
		if pos := strings.IndexAny(s, "eE"); pos >= 0 {
			e, err := strconv.Atoi(s[pos+1:])
			if err != nil || e < -300 || e > 300 {
				return
			}
		}
		want, ok := new(big.Rat).SetString(s)
		if !ok {
			return
		}
		got, exact := exactIntegerID(s)
		valid := want.IsInt() && want.Num().IsInt64()
		if exact != valid || (exact && got != want.Num().Int64()) {
			t.Fatalf("exactIntegerID(%q) = %d, %v; want %s, %v", s, got, exact, want, valid)
		}
	})
}

func BenchmarkMakeNumericID(b *testing.B) {
	for _, s := range []string{"9007199254740993", "9007199254740993.0", "90071992547409930e-1", "1e99999999999999999999"} {
		b.Run(s, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = MakeID(json.Number(s))
			}
		})
	}
}
