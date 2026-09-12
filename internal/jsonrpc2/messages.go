// Copyright 2018 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	internaljson "github.com/modelcontextprotocol/go-sdk/internal/json"
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
	data, err := jsonMarshal(&wire)
	if err != nil {
		return nil, fmt.Errorf("marshaling jsonrpc message: %w", err)
	}
	return data, nil
}

// EncodeIndent is like EncodeMessage, but honors indents.
// TODO(rfindley): refactor so that this concern is handled independently.
// Perhaps we should pass in a json.Encoder?
func EncodeIndent(msg Message, prefix, indent string) ([]byte, error) {
	wire := wireCombined{VersionTag: wireVersion}
	msg.marshal(&wire)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(&wire); err != nil {
		return nil, fmt.Errorf("marshaling jsonrpc message: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// wireDecode is the decode form of [wireCombined]. Method is a [json.RawMessage]
// so we can tell whether the "method" key was present on the wire, including
// when its value is the empty string (see go-sdk#976).
type wireDecode struct {
	VersionTag string          `json:"jsonrpc"`
	ID         json.RawMessage `json:"id"`
	Method     json.RawMessage `json:"method"`
	Params     json.RawMessage `json:"params,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      *WireError      `json:"error,omitempty"`
}

func DecodeMessage(data []byte) (Message, error) {
	msg := wireDecode{}
	if err := internaljson.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshaling jsonrpc message: %w", err)
	}
	if msg.VersionTag != wireVersion {
		return nil, fmt.Errorf("invalid message version tag %q; expected %q", msg.VersionTag, wireVersion)
	}
	id, err := DecodeID(msg.ID)
	if err != nil {
		return nil, err
	}
	if len(msg.Method) > 0 {
		// The "method" key was present. Decode its value (including "").
		var method string
		if err := internaljson.Unmarshal(msg.Method, &method); err != nil {
			return nil, fmt.Errorf("unmarshaling jsonrpc message: %w", err)
		}
		return &Request{
			Method: method,
			ID:     id,
			Params: msg.Params,
		}, nil
	}
	// no method key, should be a response
	if !id.IsValid() {
		return nil, ErrInvalidRequest
	}
	resp := &Response{
		ID:     id,
		Result: msg.Result,
	}
	// we have to check if msg.Error is nil to avoid a typed error
	if msg.Error != nil {
		resp.Error = msg.Error
	}
	return resp, nil
}

const maxIDNumberBytes = 128

// DecodeID decodes a JSON-RPC request identity without converting numeric IDs
// through float64. It is internal to the SDK and shared with MCP cancellation
// decoding.
func DecodeID(raw json.RawMessage) (ID, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ID{}, nil
	}
	if raw[0] == '"' {
		var id string
		if err := internaljson.Unmarshal(raw, &id); err != nil {
			return ID{}, fmt.Errorf("%w: invalid string ID: %v", ErrParse, err)
		}
		return StringID(id), nil
	}
	if len(raw) > maxIDNumberBytes {
		return ID{}, fmt.Errorf("%w: numeric ID exceeds %d bytes", ErrParse, maxIDNumberBytes)
	}
	id, err := parseIntegerID(string(raw))
	if err != nil {
		return ID{}, fmt.Errorf("%w: invalid numeric ID %q: %v", ErrParse, raw, err)
	}
	return Int64ID(id), nil
}

func parseIntegerID(raw string) (int64, error) {
	if raw == "" {
		return 0, errors.New("empty number")
	}

	negative := raw[0] == '-'
	if negative {
		raw = raw[1:]
		if raw == "" {
			return 0, errors.New("missing digits")
		}
	}

	mantissa, exponentText, hasExponent := strings.Cut(raw, "e")
	if !hasExponent {
		mantissa, exponentText, hasExponent = strings.Cut(raw, "E")
	}
	exponent := 0
	if hasExponent {
		var err error
		exponent, err = parseIDExponent(exponentText)
		if err != nil {
			return 0, err
		}
	}

	integerPart, fractionalPart, hasFraction := strings.Cut(mantissa, ".")
	if integerPart == "" || !allDecimalDigits(integerPart) || (hasFraction && (fractionalPart == "" || !allDecimalDigits(fractionalPart))) {
		return 0, errors.New("invalid number syntax")
	}
	if len(integerPart) > 1 && integerPart[0] == '0' {
		return 0, errors.New("invalid leading zero")
	}

	digits := strings.TrimLeft(integerPart+fractionalPart, "0")
	if digits == "" {
		return 0, nil
	}
	scale := exponent - len(fractionalPart)
	if scale < 0 {
		trim := -scale
		if trim >= len(digits) || !allZeroes(digits[len(digits)-trim:]) {
			return 0, errors.New("ID is not an integer")
		}
		digits = digits[:len(digits)-trim]
		scale = 0
	}
	if len(digits)+scale > 19 {
		return 0, errors.New("ID is outside the signed 64-bit range")
	}
	digits += strings.Repeat("0", scale)
	if negative {
		digits = "-" + digits
	}
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, errors.New("ID is outside the signed 64-bit range")
	}
	return id, nil
}

func parseIDExponent(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("missing exponent")
	}
	negative := raw[0] == '-'
	if negative || raw[0] == '+' {
		raw = raw[1:]
	}
	if raw == "" || !allDecimalDigits(raw) {
		return 0, errors.New("invalid exponent")
	}
	const limit = maxIDNumberBytes + 20
	exponent := 0
	for _, digit := range raw {
		value := int(digit - '0')
		if exponent > (limit-value)/10 {
			exponent = limit
			break
		}
		exponent = exponent*10 + value
	}
	if negative {
		exponent = -exponent
	}
	return exponent, nil
}

func allDecimalDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func allZeroes(s string) bool {
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

func marshalToRaw(obj any) (json.RawMessage, error) {
	if obj == nil {
		return nil, nil
	}
	data, err := jsonMarshal(obj)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// jsonMarshal marshals obj to JSON like json.Marshal but without HTML escaping.
func jsonMarshal(obj any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	// json.Encoder.Encode adds a trailing newline. Trim it to be consistent with json.Marshal.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
