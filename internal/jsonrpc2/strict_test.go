// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"strings"
	"testing"
)

// Test struct for unmarshalling
type testStruct struct {
	Name      string `json:"name"`
	Method    string `json:"method"`
	Arguments any    `json:"arguments,omitempty"`
}

func TestStrictUnmarshal_RejectsDuplicateKeys(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name:    "duplicate with different case - name and Name",
			json:    `{"name":"legitimate","Name":"smuggled"}`,
			wantErr: "duplicate key with different case",
		},
		{
			name:    "duplicate with different case - method and METHOD",
			json:    `{"method":"tools/call","METHOD":"secret"}`,
			wantErr: "duplicate key with different case",
		},
		{
			name:    "duplicate in nested object",
			json:    `{"name":"test","arguments":{"key":"value","Key":"smuggled"}}`,
			wantErr: "duplicate key with different case",
		},
		{
			name:    "triple duplicate with different cases",
			json:    `{"name":"a","Name":"b","NAME":"c"}`,
			wantErr: "duplicate key with different case",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result testStruct
			err := StrictUnmarshal([]byte(tt.json), &result)
			if err == nil {
				t.Errorf("StrictUnmarshal() expected error, got nil. Result: %+v", result)
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("StrictUnmarshal() error = %v, want error containing %v", err, tt.wantErr)
			}
		})
	}
}

func TestStrictUnmarshal_RejectsWrongCase(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name:    "Name instead of name",
			json:    `{"Name":"test"}`,
			wantErr: "field name case mismatch",
		},
		{
			name:    "METHOD instead of method",
			json:    `{"METHOD":"tools/call"}`,
			wantErr: "field name case mismatch",
		},
		{
			name:    "mixed case - some correct, one wrong",
			json:    `{"name":"test","METHOD":"tools/call"}`,
			wantErr: "field name case mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result testStruct
			err := StrictUnmarshal([]byte(tt.json), &result)
			if err == nil {
				t.Errorf("StrictUnmarshal() expected error, got nil. Result: %+v", result)
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("StrictUnmarshal() error = %v, want error containing %v", err, tt.wantErr)
			}
		})
	}
}

func TestStrictUnmarshal_RejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name:    "unknown field",
			json:    `{"name":"test","unknownField":"value"}`,
			wantErr: "unknown field",
		},
		{
			name:    "extra field",
			json:    `{"name":"test","method":"call","extra":"data"}`,
			wantErr: "unknown field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result testStruct
			err := StrictUnmarshal([]byte(tt.json), &result)
			if err == nil {
				t.Errorf("StrictUnmarshal() expected error, got nil. Result: %+v", result)
				return
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("StrictUnmarshal() error = %v, want error containing %v", err, tt.wantErr)
			}
		})
	}
}

func TestStrictUnmarshal_AllowsValid(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantName string
	}{
		{
			name:     "simple valid",
			json:     `{"name":"test"}`,
			wantName: "test",
		},
		{
			name:     "multiple fields",
			json:     `{"name":"greet","method":"tools/call"}`,
			wantName: "greet",
		},
		{
			name:     "with optional field",
			json:     `{"name":"test","method":"call","arguments":{"key":"value"}}`,
			wantName: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result testStruct
			err := StrictUnmarshal([]byte(tt.json), &result)
			if err != nil {
				t.Errorf("StrictUnmarshal() unexpected error = %v", err)
				return
			}
			if result.Name != tt.wantName {
				t.Errorf("StrictUnmarshal() name = %v, want %v", result.Name, tt.wantName)
			}
		})
	}
}

func TestDecodeMessage_AttackVector(t *testing.T) {
	// Test the actual attack payload from the vulnerability report
	attackPayload := `{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {
			"name": "greet",
			"Name": "secretTool",
			"arguments": {"name": "Exploit"}
		}
	}`

	_, err := DecodeMessage([]byte(attackPayload))
	if err == nil {
		t.Error("DecodeMessage() should reject attack payload with duplicate keys, got nil error")
		return
	}
	if !strings.Contains(err.Error(), "duplicate key") {
		t.Errorf("DecodeMessage() error = %v, want error containing 'duplicate key'", err)
	}
}

func TestStrictUnmarshal_NestedObjects(t *testing.T) {
	type nestedStruct struct {
		Name string `json:"name"`
		Args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"args"`
	}

	tests := []struct {
		name    string
		json    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid nested",
			json:    `{"name":"test","args":{"key":"k","value":"v"}}`,
			wantErr: false,
		},
		{
			name:    "duplicate in nested",
			json:    `{"name":"test","args":{"key":"k","Key":"smuggled"}}`,
			wantErr: true,
			errMsg:  "duplicate key",
		},
		{
			name:    "duplicate in deeply nested",
			json:    `{"name":"test","args":{"key":"k","value":"v","extra":{"a":"1","A":"2"}}}`,
			wantErr: true,
			errMsg:  "duplicate key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result nestedStruct
			err := StrictUnmarshal([]byte(tt.json), &result)
			if tt.wantErr {
				if err == nil {
					t.Errorf("StrictUnmarshal() expected error, got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("StrictUnmarshal() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("StrictUnmarshal() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestStrictUnmarshal_ArrayWithDuplicates(t *testing.T) {
	type arrayStruct struct {
		Items []map[string]string `json:"items"`
	}

	tests := []struct {
		name    string
		json    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid array",
			json:    `{"items":[{"key":"value1"},{"key":"value2"}]}`,
			wantErr: false,
		},
		{
			name:    "duplicate in array element",
			json:    `{"items":[{"key":"value","Key":"smuggled"}]}`,
			wantErr: true,
			errMsg:  "duplicate key",
		},
		{
			name:    "duplicate in second array element",
			json:    `{"items":[{"key":"value1"},{"name":"test","Name":"smuggled"}]}`,
			wantErr: true,
			errMsg:  "duplicate key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result arrayStruct
			err := StrictUnmarshal([]byte(tt.json), &result)
			if tt.wantErr {
				if err == nil {
					t.Errorf("StrictUnmarshal() expected error, got nil")
					return
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("StrictUnmarshal() error = %v, want error containing %v", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("StrictUnmarshal() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestExtractExpectedFields(t *testing.T) {
	type testCase struct {
		Field1 string `json:"field1"`
		Field2 int    `json:"field2,omitempty"`
		Field3 bool   `json:"-"` // ignored
		Field4 string // no tag
	}

	fields := extractExpectedFields(&testCase{})

	expected := map[string]bool{
		"field1": true,
		"field2": true,
	}

	if len(fields) != len(expected) {
		t.Errorf("extractExpectedFields() returned %d fields, want %d", len(fields), len(expected))
	}

	for name := range expected {
		if !fields[name] {
			t.Errorf("extractExpectedFields() missing expected field %q", name)
		}
	}

	// Should not include fields without tags or with "-" tag
	if fields["Field3"] || fields["Field4"] || fields["field4"] {
		t.Error("extractExpectedFields() should not include fields without proper json tags")
	}
}
