// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCancelledParamsRequestIDPrecision(t *testing.T) {
	for _, literal := range []string{`9007199254740993`, `-9007199254740993`, `9223372036854775807`, `-9223372036854775808`, `42`, `"9007199254740993"`, `null`} {
		t.Run(literal, func(t *testing.T) {
			data := []byte(`{"requestId":` + literal + `,"reason":"stop","_meta":{"number":42}}`)
			var params mcp.CancelledParams

			err := json.Unmarshal(data, &params)

			if err != nil {
				t.Fatal(err)
			}
			id, err := jsonrpc.MakeID(params.RequestID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(id.Raw())
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != literal {
				t.Errorf("request ID = %s, want %s", encoded, literal)
			}
			if params.Reason != "stop" {
				t.Errorf("reason = %q", params.Reason)
			}
			if params.Meta["number"] != float64(42) {
				t.Errorf("metadata decoding changed: %#v", params.Meta)
			}
		})
	}
}
