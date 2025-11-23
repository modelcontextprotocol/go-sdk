// Copyright 2018 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// ID is a Request identifier, which is defined by the spec to be a string, integer, or null.
// https://www.jsonrpc.org/specification#request_object
type ID struct {
	value any
}

// MakeID coerces the given Go value to an ID. The value should be the
// default JSON marshaling of a Request identifier: nil, float64, or string.
//
// Returns an error if the value type was not a valid Request ID type.
//
// TODO: ID can't be a json.Marshaler/Unmarshaler, because we want to omitzero.
// Simplify this package by making ID json serializable once we can rely on
// omitzero.
func MakeID(v any) (ID, error) {
	switch v := v.(type) {
	case nil:
		return ID{}, nil
	case float64:
		return Int64ID(int64(v)), nil
	case string:
		return StringID(v), nil
	}
	return ID{}, fmt.Errorf("%w: invalid ID type %T", ErrParse, v)
}

// Message is the interface to all jsonrpc2 message types.
// They share no common functionality, but are a closed set of concrete types
// that are allowed to implement this interface. The message types are *Request
// and *Response.
type Message interface {
	// marshal builds the wire form from the API form.
	// It is private, which makes the set of Message implementations closed.
	marshal(to *wireCombined)
}

// wirePool reuses wireCombined structs to reduce per-message allocations.
var wirePool = sync.Pool{
	New: func() interface{} { return new(wireCombined) },
}

// readerPool reuses bytes.Reader values so we can feed the same underlying
// []byte returned by transports into a json.Decoder without allocating a new
// reader each time.
var readerPool = sync.Pool{
	New: func() interface{} { return new(bytes.Reader) },
}

// Request is a Message sent to a peer to request behavior.
// If it has an ID it is a call, otherwise it is a notification.
type Request struct {
	// ID of this request, used to tie the Response back to the request.
	// This will be nil for notifications.
	ID ID
	// Method is a string containing the method name to invoke.
	Method string
	// Params is either a struct or an array with the parameters of the method.
	Params json.RawMessage
	// Extra is additional information that does not appear on the wire. It can be
	// used to pass information from the application to the underlying transport.
	Extra any
}

// Response is a Message used as a reply to a call Request.
// It will have the same ID as the call it is a response to.
type Response struct {
	// result is the content of the response.
	Result json.RawMessage
	// err is set only if the call failed.
	Error error
	// id of the request this is a response to.
	ID ID
	// Extra is additional information that does not appear on the wire. It can be
	// used to pass information from the underlying transport to the application.
	Extra any
}

// StringID creates a new string request identifier.
func StringID(s string) ID { return ID{value: s} }

// Int64ID creates a new integer request identifier.
func Int64ID(i int64) ID { return ID{value: i} }

// IsValid returns true if the ID is a valid identifier.
// The default value for ID will return false.
func (id ID) IsValid() bool { return id.value != nil }

// Raw returns the underlying value of the ID.
func (id ID) Raw() any { return id.value }

// NewNotification constructs a new Notification message for the supplied
// method and parameters.
func NewNotification(method string, params any) (*Request, error) {
	p, merr := marshalToRaw(params)
	return &Request{Method: method, Params: p}, merr
}

// NewCall constructs a new Call message for the supplied ID, method and
// parameters.
func NewCall(id ID, method string, params any) (*Request, error) {
	p, merr := marshalToRaw(params)
	return &Request{ID: id, Method: method, Params: p}, merr
}

func (msg *Request) IsCall() bool { return msg.ID.IsValid() }

func (msg *Request) marshal(to *wireCombined) {
	to.ID = msg.ID.value
	to.Method = msg.Method
	to.Params = msg.Params
}

// NewResponse constructs a new Response message that is a reply to the
// supplied. If err is set result may be ignored.
func NewResponse(id ID, result any, rerr error) (*Response, error) {
	r, merr := marshalToRaw(result)
	return &Response{ID: id, Result: r, Error: rerr}, merr
}

func (msg *Response) marshal(to *wireCombined) {
	to.ID = msg.ID.value
	to.Error = toWireError(msg.Error)
	to.Result = msg.Result
}

func toWireError(err error) *WireError {
	if err == nil {
		// no error, the response is complete
		return nil
	}
	if err, ok := err.(*WireError); ok {
		// already a wire error, just use it
		return err
	}
	result := &WireError{Message: err.Error()}
	var wrapped *WireError
	if errors.As(err, &wrapped) {
		// if we wrapped a wire error, keep the code from the wrapped error
		// but the message from the outer error
		result.Code = wrapped.Code
	}
	return result
}

func EncodeMessage(msg Message) ([]byte, error) {
	wire := wireCombined{VersionTag: wireVersion}
	msg.marshal(&wire)
	data, err := json.Marshal(&wire)
	if err != nil {
		return data, fmt.Errorf("marshaling jsonrpc message: %w", err)
	}
	return data, nil
}

// EncodeMessageTo writes the JSON-RPC wire encoding of msg into the
// provided buffer. It intentionally avoids allocating a temporary []byte by
// using json.Encoder to stream directly into buf. The function does not add
// a trailing newline (json.Encoder.Encode appends one), so it trims it.
//
// This helper is non-breaking: callers may use it to reduce allocations.
func EncodeMessageTo(buf *bytes.Buffer, msg Message) error {
	buf.Reset()
	wire := wireCombined{VersionTag: wireVersion}
	msg.marshal(&wire)

	enc := json.NewEncoder(buf)
	// Match default escaping behavior; SetEscapeHTML(false) reduces escapes
	// for '<', '>' which can be beneficial for performance and readability.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(&wire); err != nil {
		return fmt.Errorf("marshaling jsonrpc message: %w", err)
	}
	// json.Encoder.Encode appends a '\n' byte; remove it to match json.Marshal
	if buf.Len() > 0 {
		b := buf.Bytes()
		if b[len(b)-1] == '\n' {
			buf.Truncate(buf.Len() - 1)
		}
	}
	return nil
}

// EncodeIndent is like EncodeMessage, but honors indents.
// TODO(rfindley): refactor so that this concern is handled independently.
// Perhaps we should pass in a json.Encoder?
func EncodeIndent(msg Message, prefix, indent string) ([]byte, error) {
	wire := wireCombined{VersionTag: wireVersion}
	msg.marshal(&wire)
	data, err := json.MarshalIndent(&wire, prefix, indent)
	if err != nil {
		return data, fmt.Errorf("marshaling jsonrpc message: %w", err)
	}
	return data, nil
}

func DecodeMessage(data []byte) (Message, error) {
	// Use a pooled wireCombined to reduce allocations for the wrapper struct.
	wire := wirePool.Get().(*wireCombined)
	defer func() {
		// Clear fields to avoid retaining references; put back to pool.
		*wire = wireCombined{}
		wirePool.Put(wire)
	}()

	if err := json.Unmarshal(data, wire); err != nil {
		return nil, fmt.Errorf("unmarshaling jsonrpc message: %w", err)
	}
	if wire.VersionTag != wireVersion {
		return nil, fmt.Errorf("invalid message version tag %q; expected %q", wire.VersionTag, wireVersion)
	}
	id, err := MakeID(wire.ID)
	if err != nil {
		return nil, err
	}
	if wire.Method != "" {
		// has a method, must be a call
		return &Request{
			Method: wire.Method,
			ID:     id,
			Params: wire.Params,
		}, nil
	}
	// no method, should be a response
	if !id.IsValid() {
		return nil, ErrInvalidRequest
	}
	resp := &Response{
		ID:     id,
		Result: wire.Result,
	}
	// we have to check if wire.Error is nil to avoid a typed error
	if wire.Error != nil {
		resp.Error = wire.Error
	}
	return resp, nil
}

// DecodeMessageFrom decodes a JSON-RPC message from the provided byte slice.
// It uses a pooled bytes.Reader and the pooled wireCombined to reduce
// temporary allocations during decoding. This helper is non-breaking and is
// intended for transports that already have a []byte message buffer.
func DecodeMessageFrom(data []byte) (Message, error) {
	// Use a pooled reader to avoid allocating a new bytes.Reader.
	r := readerPool.Get().(*bytes.Reader)
	r.Reset(data)
	defer func() { r.Reset(nil); readerPool.Put(r) }()

	// Use a pooled wireCombined to reduce allocations for the wrapper struct.
	wire := wirePool.Get().(*wireCombined)
	defer func() {
		*wire = wireCombined{}
		wirePool.Put(wire)
	}()

	dec := json.NewDecoder(r)
	if err := dec.Decode(wire); err != nil {
		return nil, fmt.Errorf("unmarshaling jsonrpc message: %w", err)
	}
	if wire.VersionTag != wireVersion {
		return nil, fmt.Errorf("invalid message version tag %q; expected %q", wire.VersionTag, wireVersion)
	}
	id, err := MakeID(wire.ID)
	if err != nil {
		return nil, err
	}
	if wire.Method != "" {
		return &Request{
			Method: wire.Method,
			ID:     id,
			Params: wire.Params,
		}, nil
	}
	if !id.IsValid() {
		return nil, ErrInvalidRequest
	}
	resp := &Response{
		ID:     id,
		Result: wire.Result,
	}
	if wire.Error != nil {
		resp.Error = wire.Error
	}
	return resp, nil
}

func marshalToRaw(obj any) (json.RawMessage, error) {
	if obj == nil {
		return nil, nil
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}
