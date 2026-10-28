// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestNewDecoderCaseSensitivity(t *testing.T) {
	type Target struct {
		Field       string `json:"field"`
		TaggedField string `json:"custom_tag"`
	}

	tests := []struct {
		name string
		json string
		want Target
	}{
		{
			name: "exact match",
			json: `{"field": "value", "custom_tag": "tagged"}`,
			want: Target{Field: "value", TaggedField: "tagged"},
		},
		{
			name: "case mismatch",
			json: `{"Field": "value", "Custom_tag": "tagged"}`,
			want: Target{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Target
			dec := NewDecoder(strings.NewReader(tt.json))
			if err := dec.Decode(&got); err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Decode mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestUnmarshalNullCharacter(t *testing.T) {
	type Target struct {
		Field string `json:"field"`
	}

	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "null char in middle",
			json: `{"fi\u0000eld": "value"}`,
		},
		{
			name: "null char at end",
			json: `{"field\u0000": "value"}`,
		},
		{
			name: "null char at start",
			json: `{"\u0000field": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Target
			if err := Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			if got.Field != "" {
				t.Errorf("Field was set: %s", tt.json)
			}
		})
	}
}

func TestUnmarshalChecksNestingDepth(t *testing.T) {
	createNesting := func(depth int) []byte {
		return []byte(strings.Repeat("[", depth) + strings.Repeat("]", depth))
	}

	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{"flat object", []byte(`{"a":1}`), false},
		{"at limit", createNesting(defaultMaxDepth), false},
		{"one over limit", createNesting(defaultMaxDepth + 1), true},
		{"brackets in strings do not count", []byte(`{"a":"[[[[[[[[[["}`), false},
		{"escaped quote in string", []byte(`{"a":"\"[[[[[["}`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v any
			err := Unmarshal(tt.data, &v)
			gotDepthErr := errors.Is(err, errMaxDepthExceeded)
			if gotDepthErr != tt.wantErr {
				t.Fatalf("Unmarshal() error = %v (err=%v), want depth error = %v", gotDepthErr, err, tt.wantErr)
			}
		})
	}
}

func TestUnmarshalMaxDepthExitsFast(t *testing.T) {
	data := []byte(strings.Repeat("[", 500_000) + strings.Repeat("]", 500_000))
	done := make(chan error, 1)
	go func() {
		var v any
		done <- Unmarshal(data, &v)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errMaxDepthExceeded) {
			t.Fatalf("Unmarshal error = %v, want %v", err, errMaxDepthExceeded)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Unmarshal did not reject deeply nested payload promptly")
	}
}

func FuzzUnmarshalKeyComparison(f *testing.F) {
	// Add seed corpus for expected valid key and the known problematic cases.
	f.Add("field")
	f.Add("FIELD")
	f.Add("field\x00")

	f.Fuzz(func(t *testing.T, key string) {
		// Generate valid JSON with the fuzzed key.
		// json.Encoder ensures the key is properly escaped (e.g. \x00 becomes \u0000).
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(key); err != nil {
			return
		}
		encodedKey := buf.Bytes()
		jsonData := fmt.Sprintf(`{%s: "value"}`, encodedKey)

		type Target struct {
			Field string `json:"field"`
		}

		var got Target
		if err := Unmarshal([]byte(jsonData), &got); err != nil {
			return
		}

		if got.Field == "value" && key != "field" {
			t.Errorf("Unmarshal matched key %q (JSON: %s) to field 'field'", key, encodedKey)
		}
	})
}

func FuzzUnmarshalFieldIsolation(f *testing.F) {
	// Add seed corpus.
	f.Add("other", "value")
	f.Add("safe", "overwrite")
	f.Add("safe\x00", "attack")

	f.Fuzz(func(t *testing.T, key, val string) {
		// Generate valid JSON with the fuzzed key and value.
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(key); err != nil {
			return
		}
		encodedKey := buf.Bytes()
		buf.Reset()
		if err := enc.Encode(val); err != nil {
			return
		}
		encodedVal := buf.Bytes()

		jsonData := fmt.Sprintf(`{"safe": "value", %s: %s}`, encodedKey, encodedVal)

		type Target struct {
			Safe string `json:"safe"`
		}

		var got Target
		if err := Unmarshal([]byte(jsonData), &got); err != nil {
			return
		}

		if got.Safe != "value" && key != "safe" {
			t.Errorf("Field 'safe' improperly modified by key %q (JSON: %s). Got %q, want %q", key, encodedKey, got.Safe, "value")
		}
	})
}

// decodeStream is how Unmarshal decoded before it parsed data in place:
// through the package's Decoder, which allocates a read buffer of at least
// 32 KiB for every call. Comparing with it also keeps [parseFlags] in step
// with [newDecoder].
func decodeStream(data []byte, v any, useNumber bool) error {
	if err := checkMaxDepth(data, defaultMaxDepth); err != nil {
		return err
	}
	return newDecoder(bytes.NewReader(data), useNumber).Decode(v)
}

type unmarshalTarget struct {
	ID     any               `json:"id"`
	Method string            `json:"method"`
	Params map[string]any    `json:"params"`
	Raw    json.RawMessage   `json:"raw"`
	Nested *unmarshalTarget  `json:"nested"`
	List   []int             `json:"list"`
	Map    map[string]string `json:"map"`
}

// sameAsStream checks that Unmarshal and UnmarshalUseNumber decode data
// exactly as the streaming Decoder did: the same value, and the same error,
// by type and by text.
func sameAsStream(t *testing.T, data []byte) {
	t.Helper()
	for _, useNumber := range []bool{false, true} {
		for _, newTarget := range []func() any{
			func() any { return new(any) },
			func() any { return new(unmarshalTarget) },
			func() any { return new(map[string]any) },
			func() any { return new([]any) },
			func() any { return new(string) },
		} {
			want, got := newTarget(), newTarget()
			wantErr := decodeStream(data, want, useNumber)
			var gotErr error
			if useNumber {
				gotErr = UnmarshalUseNumber(data, got)
			} else {
				gotErr = Unmarshal(data, got)
			}
			if fmt.Sprintf("%T %v", gotErr, gotErr) != fmt.Sprintf("%T %v", wantErr, wantErr) {
				t.Fatalf("%q into %T (useNumber %v): error %T %v, want %T %v", data, got, useNumber, gotErr, gotErr, wantErr, wantErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%q into %T (useNumber %v): got %#v, want %#v", data, got, useNumber, got, want)
			}
		}
	}
}

var unmarshalCases = []string{
	``, ` `, "\n\t ", `{}`, `[]`, `null`, `true`, `1`, `-0.5e3`, `"s"`,
	`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get","arguments":{"a":[1,2,{"b":null}]}}}`,
	`  {"id":"x"}  `, `{"id":1} {"id":2}`, `{"id":1}garbage`, `{"id":1}]`, `[1,2]x`,
	`{"id":`, `{"id":1`, `[1,2`, `"unterminated`, `{"id":1,}`, `{"id" 1}`, `{,}`, `]`, `}`, `x`, `nul`, `tru`, `1.`,
	`{"list":[1,"two"]}`, `{"method":1}`, `{"nested":{"nested":{"method":[]}}}`, `{"raw":{"keep":"as is"}}`,
	`{"Method":"case"}`, `{"METHOD":"case"}`, `{"map":{"a":"b","a":"c"}}`, `{"id":12345678901234567890}`,
	`{"id":1.5}`, `{"params":{"n":1e400}}`, "{\"method\":\"nul\x00l\"}", `{"method":"é😀"}`,
	`{"method":"\ud800"}`, "\xff", `{"method":"` + "\xff" + `"}`, `[[[[[]]]]]`, "\ufeff{}",
}

func TestUnmarshalSameAsStream(t *testing.T) {
	for _, c := range unmarshalCases {
		sameAsStream(t, []byte(c))
	}
}

func FuzzUnmarshalSameAsStream(f *testing.F) {
	for _, c := range unmarshalCases {
		f.Add([]byte(c))
	}
	f.Fuzz(sameAsStream)
}

// UnmarshalUseNumber matches field names case-sensitively, like Unmarshal:
// jsonrpc2.DecodeMessage decodes every message envelope with it, and a
// case-insensitive match would let "Method" override "method".
func TestUnmarshalUseNumberCaseSensitivity(t *testing.T) {
	var got struct {
		Method string `json:"method"`
		ID     any    `json:"id"`
	}
	data := []byte(`{"method":"tools/list","Method":"tools/call","ID":2,"id":9007199254740993}`)
	if err := UnmarshalUseNumber(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != "tools/list" || got.ID != json.Number("9007199254740993") {
		t.Errorf("got method %q, id %#v; want \"tools/list\" and json.Number(\"9007199254740993\")", got.Method, got.ID)
	}
}

// Decoded values never share memory with the input: Unmarshal finds the
// value in place, and the caller may reuse the input buffer afterwards.
func TestUnmarshalDoesNotAliasInput(t *testing.T) {
	const input = `{"s":"hello","raw":{"a":"b"},"n":12345,"any":{"k":"v"}}`
	data := []byte(input)
	var v struct {
		S   string          `json:"s"`
		Raw json.RawMessage `json:"raw"`
		N   json.Number     `json:"n"`
		Any any             `json:"any"`
	}
	if err := UnmarshalUseNumber(data, &v); err != nil {
		t.Fatal(err)
	}
	var whole json.RawMessage
	if err := Unmarshal(data, &whole); err != nil {
		t.Fatal(err)
	}
	for i := range data {
		data[i] = 'X'
	}
	if v.S != "hello" || string(v.Raw) != `{"a":"b"}` || v.N != "12345" || string(whole) != input {
		t.Errorf("decoded values changed with the input: %+v, %s", v, whole)
	}
	if m, ok := v.Any.(map[string]any); !ok || m["k"] != "v" {
		t.Errorf("decoded map changed with the input: %v", v.Any)
	}
}

// Unmarshal allocates in proportion to its input, not a stream buffer.
func BenchmarkUnmarshal(b *testing.B) {
	data := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_todo","arguments":{"name":"todos/1"}}}`)
	b.ReportAllocs()
	for b.Loop() {
		var v unmarshalTarget
		if err := Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}
