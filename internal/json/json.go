// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// Package json provides internal JSON utilities.

package json

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	jsonv1 "github.com/go-json-experiment/json/v1"
)

// defaultMaxDepth is the maximum JSON nesting depth accepted by [Unmarshal].
// Realistic MCP payloads nest only a handful of levels, so 1000 is far more
// than any legitimate message requires while keeping worst-case parse cost
// bounded.
const defaultMaxDepth = 1000

// errMaxDepthExceeded is returned by [Unmarshal] when the input nests deeper than [defaultMaxDepth].
var errMaxDepthExceeded = fmt.Errorf("json: exceeded maximum nesting depth of %d", defaultMaxDepth)

var caseSensitiveV1 = jsonv2.JoinOptions(
	jsonv1.DefaultOptionsV1(),
	jsonv2.MatchCaseInsensitiveNames(false),
)

// caseSensitiveV1UseNumber adds UseNumber, which v2 has no option for: a JSON
// number decoded into an any becomes a json.Number, so large IDs stay exact.
var caseSensitiveV1UseNumber = jsonv2.JoinOptions(
	caseSensitiveV1,
	jsonv2.WithUnmarshalers(jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, v *any) error {
		if dec.PeekKind() != '0' {
			return errors.ErrUnsupported
		}
		if p := reflect.ValueOf(*v); p.Kind() == reflect.Pointer && !p.IsNil() {
			return errors.ErrUnsupported
		}
		val, err := dec.ReadValue()
		if err != nil {
			return err
		}
		*v = json.Number(string(val))
		return nil
	})),
)

type Decoder struct {
	dec  *jsontext.Decoder
	opts jsonv2.Options
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: jsontext.NewDecoder(r), opts: caseSensitiveV1}
}

func (d *Decoder) Decode(v any) error {
	return jsonv2.UnmarshalDecode(d.dec, v, d.opts)
}

func Unmarshal(data []byte, v any) error {
	if err := checkMaxDepth(data, defaultMaxDepth); err != nil {
		return err
	}
	return NewDecoder(bytes.NewReader(data)).Decode(v)
}

func (d *Decoder) UseNumber() { d.opts = caseSensitiveV1UseNumber }

func UnmarshalUseNumber(data []byte, v any) error {
	if err := checkMaxDepth(data, defaultMaxDepth); err != nil {
		return err
	}
	dec := NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(v)
}

// checkMaxDepth scans data once and reports [errMaxDepthExceeded] if the
// nesting of JSON objects and arrays exceeds maxDepth. It is a lightweight pass which
// tracks '{' and '[' against '}' and ']' while skipping over the contents of strings.
func checkMaxDepth(data []byte, maxDepth int) error {
	var (
		depth    int
		inString bool
		escaped  bool
	)
	for _, b := range data {
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > maxDepth {
				return errMaxDepthExceeded
			}
		case '}', ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return nil
}
