// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import "testing"

func TestEncodeHeaderValue(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		want   string
		wantOK bool
	}{
		// Strings
		{"plain ASCII", "us-west1", "us-west1", true},
		{"empty string", "", "", true},
		{"string with internal spaces", "us west 1", "us west 1", true},
		{"string with leading space", " us-west1", "=?base64?IHVzLXdlc3Qx?=", true},
		{"string with trailing space", "us-west1 ", "=?base64?dXMtd2VzdDEg?=", true},
		{"string with both spaces", " us-west1 ", "=?base64?IHVzLXdlc3QxIA==?=", true},
		{"non-ASCII", "日本語", "=?base64?5pel5pys6Kqe?=", true},
		{"mixed ASCII and non-ASCII", "Hello, 世界", "=?base64?SGVsbG8sIOS4lueVjA==?=", true},
		{"string with newline", "line1\nline2", "=?base64?bGluZTEKbGluZTI=?=", true},
		{"string with carriage return", "line1\r\nline2", "=?base64?bGluZTENCmxpbmUy?=", true},
		{"string with leading tab", "\tindented", "=?base64?CWluZGVudGVk?=", true},

		// Numbers
		{"integer", float64(42), "42", true},
		{"float", float64(3.14159), "3.14159", true},

		// Booleans
		{"true", true, "true", true},
		{"false", false, "false", true},

		// Unsupported types
		{"nil", nil, "", false},
		{"slice", []string{"a"}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := encodeHeaderValue(tt.value)
			if ok != tt.wantOK {
				t.Fatalf("encodeHeaderValue(%v) ok = %v, want %v", tt.value, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("encodeHeaderValue(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestDecodeHeaderValue(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{"plain value", "us-west1", "us-west1", true},
		{"empty value", "", "", true},
		{"valid base64", "=?base64?SGVsbG8=?=", "Hello", true},
		{"non-ASCII decoded", "=?base64?5pel5pys6Kqe?=", "日本語", true},
		{"leading space decoded", "=?base64?IHVzLXdlc3Qx?=", " us-west1", true},
		{"case-insensitive prefix", "=?BASE64?SGVsbG8=?=", "Hello", true},
		{"invalid base64 chars", "=?base64?SGVs!!!bG8=?=", "", false},
		// Missing prefix or suffix: treated as literal values, not base64
		{"missing prefix", "SGVsbG8=", "SGVsbG8=", true},
		{"missing suffix", "=?base64?SGVsbG8=", "=?base64?SGVsbG8=", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeHeaderValue(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("decodeHeaderValue(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("decodeHeaderValue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	values := []string{
		"us-west1",
		"",
		" leading",
		"trailing ",
		"Hello, 世界",
		"line1\nline2",
		"\ttab",
	}
	for _, v := range values {
		encoded, ok := encodeHeaderValue(v)
		if !ok {
			t.Fatalf("encodeHeaderValue(%q) failed", v)
		}
		decoded, ok := decodeHeaderValue(encoded)
		if !ok {
			t.Fatalf("decodeHeaderValue(%q) failed", encoded)
		}
		if decoded != v {
			t.Errorf("round-trip failed: %q -> %q -> %q", v, encoded, decoded)
		}
	}
}
