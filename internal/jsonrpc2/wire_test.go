// Copyright 2020 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
)

func TestWireMessage(t *testing.T) {
	for _, test := range []struct {
		name    string
		msg     jsonrpc2.Message
		encoded []byte
	}{{
		name:    "notification",
		msg:     newNotification("alive", nil),
		encoded: []byte(`{"jsonrpc":"2.0","method":"alive"}`),
	}, {
		name:    "call",
		msg:     newCall("msg1", "ping", nil),
		encoded: []byte(`{"jsonrpc":"2.0","id":"msg1","method":"ping"}`),
	}, {
		name:    "response",
		msg:     newResponse("msg2", "pong", nil),
		encoded: []byte(`{"jsonrpc":"2.0","id":"msg2","result":"pong"}`),
	}, {
		name:    "numerical id",
		msg:     newCall(1, "poke", nil),
		encoded: []byte(`{"jsonrpc":"2.0","id":1,"method":"poke"}`),
	}, {
		// originally reported in #39719, this checks that result is not present if
		// it is an error response
		name: "computing fix edits",
		msg:  newResponse(3, nil, jsonrpc2.NewError(0, "computing fix edits")),
		encoded: []byte(`{
		"jsonrpc":"2.0",
		"id":3,
		"error":{
			"code":0,
			"message":"computing fix edits"
		}
	}`),
	}} {
		b, err := jsonrpc2.EncodeMessage(test.msg)
		if err != nil {
			t.Fatal(err)
		}
		checkJSON(t, b, test.encoded)
		msg, err := jsonrpc2.DecodeMessage(test.encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(msg, test.msg) {
			t.Errorf("decoded message does not match\nGot:\n%+#v\nWant:\n%+#v", msg, test.msg)
		}
	}
}

func newNotification(method string, params any) jsonrpc2.Message {
	msg, err := jsonrpc2.NewNotification(method, params)
	if err != nil {
		panic(err)
	}
	return msg
}

func newID(id any) jsonrpc2.ID {
	switch v := id.(type) {
	case nil:
		return jsonrpc2.ID{}
	case string:
		return jsonrpc2.StringID(v)
	case int:
		return jsonrpc2.Int64ID(int64(v))
	case int64:
		return jsonrpc2.Int64ID(v)
	default:
		panic("invalid ID type")
	}
}

func newCall(id any, method string, params any) jsonrpc2.Message {
	msg, err := jsonrpc2.NewCall(newID(id), method, params)
	if err != nil {
		panic(err)
	}
	return msg
}

func newResponse(id any, result any, rerr error) jsonrpc2.Message {
	msg, err := jsonrpc2.NewResponse(newID(id), result, rerr)
	if err != nil {
		panic(err)
	}
	return msg
}

// go-sdk#976: empty method must decode as a request (encode omits empty method).
func TestDecodeEmptyMethodRequest(t *testing.T) {
	encoded := []byte(`{"jsonrpc":"2.0","id":5,"method":"","params":{}}`)
	msg, err := jsonrpc2.DecodeMessage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	req, ok := msg.(*jsonrpc2.Request)
	if !ok {
		t.Fatalf("message type = %T, want *jsonrpc2.Request", msg)
	}
	if req.Method != "" {
		t.Errorf("Method = %q, want empty string", req.Method)
	}
	if req.ID != jsonrpc2.Int64ID(5) {
		t.Errorf("ID = %v, want 5", req.ID.Raw())
	}
	if !req.IsCall() {
		t.Error("empty method with id=5 should be a call")
	}
}

func TestDecodeResponseUnchanged(t *testing.T) {
	encoded := []byte(`{"jsonrpc":"2.0","id":2,"result":{}}`)
	msg, err := jsonrpc2.DecodeMessage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := msg.(*jsonrpc2.Response); !ok {
		t.Fatalf("message type = %T, want *jsonrpc2.Response", msg)
	}
}

func TestMakeIDRejectsNonInteger(t *testing.T) {
	for _, test := range []struct {
		name string
		id   any
	}{
		{name: "fractional", id: 1.9},
		{name: "half", id: 2.5},
		{name: "above int64 range", id: 9.3e18},
		{name: "below int64 range", id: -9.3e18},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := jsonrpc2.MakeID(test.id); err == nil {
				t.Errorf("MakeID(%v) = nil error, want ErrParse", test.id)
			}
		})
	}
}

func TestMakeIDAcceptsInteger(t *testing.T) {
	for _, test := range []struct {
		name string
		id   any
		want int64
	}{
		{name: "zero", id: float64(0), want: 0},
		{name: "positive", id: float64(42), want: 42},
		{name: "negative", id: float64(-7), want: -7},
		{name: "max exact float64 integer", id: float64(1 << 53), want: 1 << 53},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, err := jsonrpc2.MakeID(test.id)
			if err != nil {
				t.Fatalf("MakeID(%v) = error: %v", test.id, err)
			}
			if got := id.Raw(); got != test.want {
				t.Errorf("MakeID(%v).Raw() = %v, want %v", test.id, got, test.want)
			}
		})
	}
}

func TestDecodeMessageRejectsFractionalID(t *testing.T) {
	encoded := []byte(`{"jsonrpc":"2.0","id":1.9,"method":"ping"}`)
	if _, err := jsonrpc2.DecodeMessage(encoded); err == nil {
		t.Fatal("DecodeMessage with fractional id = nil error, want error")
	}
}

// Messages with an id but no "method" key are responses, not malformed requests.
func TestDecodeIDOnlyMessageIsResponse(t *testing.T) {
	encoded := []byte(`{"jsonrpc":"2.0","id":5}`)
	msg, err := jsonrpc2.DecodeMessage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := msg.(*jsonrpc2.Response); !ok {
		t.Fatalf("message type = %T, want *jsonrpc2.Response", msg)
	}
}

// TestWireErrorIs checks the errors.Is behavior of WireError, including that
// comparing against a typed-nil *WireError does not panic.
func TestWireErrorIs(t *testing.T) {
	err := jsonrpc2.NewError(-32600, "invalid request")
	for _, test := range []struct {
		name   string
		target error
		want   bool
	}{
		{name: "same code", target: jsonrpc2.NewError(-32600, "other message"), want: true},
		{name: "different code", target: jsonrpc2.NewError(-32601, "invalid request"), want: false},
		{name: "typed nil WireError", target: (*jsonrpc2.WireError)(nil), want: false},
		{name: "untyped nil", target: nil, want: false},
		{name: "other error type", target: errors.New("invalid request"), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := errors.Is(err, test.target); got != test.want {
				t.Errorf("errors.Is(%v, %v) = %v, want %v", err, test.target, got, test.want)
			}
		})
	}
}

func checkJSON(t *testing.T, got, want []byte) {
	// compare the compact form, to allow for formatting differences
	g := &bytes.Buffer{}
	if err := json.Compact(g, []byte(got)); err != nil {
		t.Fatal(err)
	}
	w := &bytes.Buffer{}
	if err := json.Compact(w, []byte(want)); err != nil {
		t.Fatal(err)
	}
	if g.String() != w.String() {
		t.Errorf("encoded message does not match\nGot:\n%s\nWant:\n%s", g, w)
	}
}
