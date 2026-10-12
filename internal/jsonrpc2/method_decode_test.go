// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"strings"
	"testing"
)

func TestDecodeMethodValue(t *testing.T) {
	for _, tt := range []struct {
		name, value, want string
		invalid           bool
	}{
		{"plain", `"tools/call"`, "tools/call", false},
		{"empty", `""`, "", false},
		{"escaped", `"tools\u002fcall"`, "tools/call", false},
		{"unicode", `"\ud83d\ude00"`, "😀", false},
		{"null", `null`, "", false},
		{"number", `42`, "", true},
		{"boolean", `true`, "", true},
		{"object", `{}`, "", true},
		{"array", `[]`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			wire := []byte(`{"jsonrpc":"2.0","id":1,"method":` + tt.value + `}`)
			// Act.
			msg, err := DecodeMessage(wire)
			// Assert.
			if tt.invalid {
				if err == nil {
					t.Fatal("expected invalid method error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			req, ok := msg.(*Request)
			if !ok || req.Method != tt.want {
				t.Fatalf("decoded message = %#v, want request method %q", msg, tt.want)
			}
		})
	}
}

func TestDecodeMethodEnvelopeGuards(t *testing.T) {
	t.Run("case sensitive field", func(t *testing.T) {
		// Arrange.
		wire := []byte(`{"jsonrpc":"2.0","id":1,"Method":"ping","result":{}}`)
		// Act.
		msg, err := DecodeMessage(wire)
		// Assert.
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := msg.(*Response); !ok {
			t.Fatalf("decoded %T, want response", msg)
		}
	})
	t.Run("depth limit", func(t *testing.T) {
		// Arrange.
		wire := []byte(`{"jsonrpc":"2.0","id":1,"method":` + strings.Repeat("[", 1001) + `0` + strings.Repeat("]", 1001) + `}`)
		// Act.
		_, err := DecodeMessage(wire)
		// Assert.
		if err == nil || !strings.Contains(err.Error(), "maximum nesting depth") {
			t.Fatalf("error = %v, want depth rejection", err)
		}
	})
}

func BenchmarkDecodeMethodRequest(b *testing.B) {
	for _, method := range []string{`"tools/call"`, `"tools\u002fcall"`} {
		b.Run(method, func(b *testing.B) {
			wire := []byte(`{"jsonrpc":"2.0","id":1,"method":` + method + `,"params":{}}`)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DecodeMessage(wire); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
