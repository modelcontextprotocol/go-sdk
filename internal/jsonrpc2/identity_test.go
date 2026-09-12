// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package jsonrpc2

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDecodeMessageIdentity(t *testing.T) {
	tests := []struct {
		name    string
		wire    string
		want    any
		wantErr bool
	}{
		{name: "string", wire: `"request-1"`, want: "request-1"},
		{name: "numeric string", wire: `"9007199254740993"`, want: "9007199254740993"},
		{name: "zero", wire: `0`, want: int64(0)},
		{name: "negative", wire: `-17`, want: int64(-17)},
		{name: "large adjacent low", wire: `9007199254740992`, want: int64(9007199254740992)},
		{name: "large adjacent high", wire: `9007199254740993`, want: int64(9007199254740993)},
		{name: "maximum", wire: `9223372036854775807`, want: int64(9223372036854775807)},
		{name: "minimum", wire: `-9223372036854775808`, want: int64(-9223372036854775808)},
		{name: "integral fraction", wire: `9007199254740993.0`, want: int64(9007199254740993)},
		{name: "integral exponent", wire: `900719925474099300e-2`, want: int64(9007199254740993)},
		{name: "fractional", wire: `1.5`, wantErr: true},
		{name: "fractional exponent", wire: `1e-1`, wantErr: true},
		{name: "positive overflow", wire: `9223372036854775808`, wantErr: true},
		{name: "negative overflow", wire: `-9223372036854775809`, wantErr: true},
		{name: "large exponent", wire: `1e999999999999999999`, wantErr: true},
		{name: "boolean", wire: `true`, wantErr: true},
		{name: "object", wire: `{}`, wantErr: true},
		{name: "array", wire: `[]`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, message := range []string{
				`{"jsonrpc":"2.0","id":` + test.wire + `,"method":"test"}`,
				`{"jsonrpc":"2.0","id":` + test.wire + `,"result":null}`,
			} {
				got, err := DecodeMessage([]byte(message))
				if test.wantErr {
					if !errors.Is(err, ErrParse) {
						t.Fatalf("DecodeMessage() error = %v, want ErrParse", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				var id ID
				switch got := got.(type) {
				case *Request:
					id = got.ID
				case *Response:
					id = got.ID
				default:
					t.Fatalf("DecodeMessage() type = %T", got)
				}
				if id.Raw() != test.want {
					t.Fatalf("ID = %#v, want %#v", id.Raw(), test.want)
				}
				encoded, err := EncodeMessage(got)
				if err != nil {
					t.Fatal(err)
				}
				roundTrip, err := DecodeMessage(encoded)
				if err != nil {
					t.Fatal(err)
				}
				var roundTripID ID
				switch got := roundTrip.(type) {
				case *Request:
					roundTripID = got.ID
				case *Response:
					roundTripID = got.ID
				}
				if roundTripID != id {
					t.Fatalf("round-trip ID = %#v, want %#v", roundTripID.Raw(), id.Raw())
				}
			}
		})
	}
}

func TestDecodeMessageNotificationIdentity(t *testing.T) {
	for _, wire := range []string{
		`{"jsonrpc":"2.0","method":"notify"}`,
		`{"jsonrpc":"2.0","id":null,"method":"notify"}`,
	} {
		msg, err := DecodeMessage([]byte(wire))
		if err != nil {
			t.Fatal(err)
		}
		if msg.(*Request).IsCall() {
			t.Fatalf("DecodeMessage(%s) produced a call, want notification", wire)
		}
	}
	if _, err := DecodeMessage([]byte(`{"jsonrpc":"2.0","id":null,"result":null}`)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("null response ID error = %v, want ErrInvalidRequest", err)
	}
}

func TestDecodedNumericAndStringIDsAreDistinct(t *testing.T) {
	numeric := mustDecodeIdentityMessage(t, `{"jsonrpc":"2.0","id":9007199254740993,"method":"test"}`).(*Request).ID
	text := mustDecodeIdentityMessage(t, `{"jsonrpc":"2.0","id":"9007199254740993","method":"test"}`).(*Request).ID
	if numeric == text {
		t.Fatalf("numeric ID %#v aliases string ID %#v", numeric.Raw(), text.Raw())
	}
}

func TestConnectionCorrelatesAdjacentLargeIDsOutOfOrder(t *testing.T) {
	reader := newIdentityTestReader()
	writer := &identityTestWriter{messages: make(chan Message, 2)}
	conn := NewConnection(context.Background(), ConnectionConfig{
		Reader: reader,
		Writer: writer,
		Closer: reader,
		Bind: func(*Connection) Handler {
			return HandlerFunc(func(context.Context, *Request) (any, error) {
				return nil, ErrNotHandled
			})
		},
	})
	t.Cleanup(func() { _ = conn.Close() })

	atomic.StoreInt64(&conn.seq, 9007199254740991)
	low := conn.Call(context.Background(), "low", nil)
	high := conn.Call(context.Background(), "high", nil)
	for range 2 {
		<-writer.messages
	}

	reader.messages <- mustDecodeIdentityMessage(t, `{"jsonrpc":"2.0","id":9007199254740993,"result":"high"}`)
	reader.messages <- mustDecodeIdentityMessage(t, `{"jsonrpc":"2.0","id":9007199254740992,"result":"low"}`)

	var lowResult, highResult string
	if err := high.Await(context.Background(), &highResult); err != nil {
		t.Fatal(err)
	}
	if err := low.Await(context.Background(), &lowResult); err != nil {
		t.Fatal(err)
	}
	if lowResult != "low" || highResult != "high" {
		t.Fatalf("results = (%q, %q), want (low, high)", lowResult, highResult)
	}
}

type identityTestReader struct {
	messages chan Message
	done     chan struct{}
	once     sync.Once
}

func newIdentityTestReader() *identityTestReader {
	return &identityTestReader{messages: make(chan Message, 4), done: make(chan struct{})}
}

func (r *identityTestReader) Read(ctx context.Context) (Message, error) {
	select {
	case msg := <-r.messages:
		return msg, nil
	case <-r.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *identityTestReader) Close() error {
	r.once.Do(func() { close(r.done) })
	return nil
}

type identityTestWriter struct {
	messages chan Message
}

func (w *identityTestWriter) Write(ctx context.Context, msg Message) error {
	select {
	case w.messages <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mustDecodeIdentityMessage(t *testing.T, wire string) Message {
	t.Helper()
	msg, err := DecodeMessage([]byte(wire))
	if err != nil {
		t.Fatal(err)
	}
	return msg
}
