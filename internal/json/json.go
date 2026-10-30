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
	return newDecoder(r, false)
}

// newDecoder returns a Decoder that decodes as every function of this
// package does: field names match case-sensitively, and with useNumber,
// numbers decode to json.Number. [parseFlags] must stay in step with it.
func newDecoder(r io.Reader, useNumber bool) *Decoder {
	dec := json.NewDecoder(r)
	dec.DontMatchCaseInsensitiveStructFields()
	if useNumber {
		dec.UseNumber()
	}
	return &Decoder{dec: dec}
}

// parseFlags returns the [json.Parse] flags that decode as a Decoder from
// [newDecoder] does.
func parseFlags(useNumber bool) json.ParseFlags {
	flags := json.DontMatchCaseInsensitiveStructFields
	if useNumber {
		flags |= json.UseNumber
	}
	return flags
}

func (d *Decoder) Decode(v any) error {
	return d.dec.Decode(v)
}

func Unmarshal(data []byte, v any) error {
	return unmarshal(data, v, false)
}

func UnmarshalUseNumber(data []byte, v any) error {
	return unmarshal(data, v, true)
}

// unmarshal decodes the first JSON value in data into v exactly as a
// [Decoder] reading data does, but without the Decoder's read buffer of at
// least 32 KiB. Like the Decoder, it first finds the extent of the value,
// here in place, then decodes just that value. Malformed input still goes
// through a Decoder, so its error is exactly the Decoder's.
func unmarshal(data []byte, v any, useNumber bool) error {
	if err := checkMaxDepth(data, defaultMaxDepth); err != nil {
		return err
	}
	flags := parseFlags(useNumber)
	var value json.RawMessage
	if _, err := json.Parse(data, &value, flags|json.DontCopyRawMessage); err != nil {
		return newDecoder(bytes.NewReader(data), useNumber).Decode(v)
	}
	_, err := json.Parse(value, v, flags)
	return err
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
