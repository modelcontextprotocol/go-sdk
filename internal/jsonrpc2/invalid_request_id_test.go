// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestInvalidRequestIDClassification(t *testing.T) {
	for _, tt := range []struct {
		name, wire                string
		rejectedRequest, rejected bool
	}{
		{"request", `{"jsonrpc":"2.0","method":"ping","id":true}`, true, true},
		{"response", `{"jsonrpc":"2.0","result":{},"id":true}`, false, true},
		{"syntax", `{"jsonrpc":`, false, true},
		{"notification", `{"jsonrpc":"2.0","method":"ping"}`, false, false},
		{"null", `{"jsonrpc":"2.0","method":"ping","id":null}`, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			wire := []byte(tt.wire)
			// Act.
			_, err := DecodeMessage(wire)
			// Assert.
			if (err != nil) != tt.rejected || errors.Is(err, ErrInvalidRequestID) != tt.rejectedRequest {
				t.Fatalf("DecodeMessage error = %v", err)
			}
		})
	}
}

func TestUnknownIDErrorEncoding(t *testing.T) {
	// Arrange.
	response := &Response{Error: ErrInvalidRequest}
	// Act.
	wire, err := EncodeMessage(response)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["id"]) != "null" || fields["error"] == nil || fields["result"] != nil {
		t.Fatalf("invalid error response: %s", wire)
	}
}
