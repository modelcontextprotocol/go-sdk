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

func TestNullIDErrorResponseRoundTrip(t *testing.T) {
	// Arrange.
	original := &Response{Error: &WireError{Code: -32600, Message: "invalid request ID"}}
	// Act.
	wire, err := EncodeMessage(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMessage(wire)
	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	response, ok := decoded.(*Response)
	if !ok {
		t.Fatalf("decoded %T, want response", decoded)
	}
	var wireErr *WireError
	if response.ID.IsValid() || !errors.As(response.Error, &wireErr) || wireErr.Code != -32600 {
		t.Fatalf("response = %#v", response)
	}
}

func TestInvalidUnknownIDResponse(t *testing.T) {
	for _, wire := range []string{
		`{"jsonrpc":"2.0","error":{"code":-32600,"message":"bad"}}`,
		`{"jsonrpc":"2.0","id":null,"result":{}}`,
		`{"jsonrpc":"2.0","id":null,"error":null}`,
		`{"jsonrpc":"2.0","id":null,"result":null,"error":{"code":-32600,"message":"bad"}}`,
		`{"jsonrpc":"2.0","ID":null,"error":{"code":-32600,"message":"bad"}}`,
	} {
		// Arrange / Act.
		_, err := DecodeMessage([]byte(wire))
		// Assert.
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("DecodeMessage(%s) error = %v", wire, err)
		}
	}
}
