// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// Package json provides internal JSON utilities.

package json

import (
	"bytes"
	"fmt"
	"io"

	"github.com/segmentio/encoding/json"
)

// defaultMaxDepth is the maximum JSON nesting depth accepted by [Unmarshal].
// Realistic MCP payloads nest only a handful of levels, so 1000 is far more
// than any legitimate message requires while keeping worst-case parse cost
// bounded.
const defaultMaxDepth = 1000

// errMaxDepthExceeded is returned by [Unmarshal] when the input nests deeper than [defaultMaxDepth].
var errMaxDepthExceeded = fmt.Errorf("json: exceeded maximum nesting depth of %d", defaultMaxDepth)

type Decoder struct {
	dec *json.Decoder
}

func NewDecoder(r io.Reader) *Decoder {
	dec := json.NewDecoder(r)
	dec.DontMatchCaseInsensitiveStructFields()
	return &Decoder{dec: dec}
}

func (d *Decoder) Decode(v any) error {
	return d.dec.Decode(v)
}

func Unmarshal(data []byte, v any) error {
	if err := checkMaxDepth(data, defaultMaxDepth); err != nil {
		return err
	}
	return NewDecoder(bytes.NewReader(data)).Decode(v)
}

// UnmarshalUseNumber is Unmarshal with UseNumber set, so that JSON numbers
// decoded into an `any` become json.Number rather than float64.
//
// Decoding into float64 is lossy above 2^53: 9007199254740993 comes back as
// 9007199254740992, and a caller that re-marshals the decoded value writes
// the wrong number out with nothing reporting a problem. A caller decoding
// into a typed struct is unaffected either way, since UseNumber does not
// change how a number is decoded into a concrete numeric field.
//
// json.Number is a string type, so a decoded value is NOT yet suitable for
// jsonschema validation, which reports it as a string where an integer or a
// number is wanted. See mcp.narrowNumbers.
func UnmarshalUseNumber(data []byte, v any) error {
	if err := checkMaxDepth(data, defaultMaxDepth); err != nil {
		return err
	}
	dec := NewDecoder(bytes.NewReader(data))
	dec.dec.UseNumber()
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
