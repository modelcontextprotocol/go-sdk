// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	internaljson "github.com/modelcontextprotocol/go-sdk/internal/json"
)

// A ToolHandler handles a call to tools/call.
//
// This is a low-level API, for use with [Server.AddTool]. It does not do any
// pre- or post-processing of the request or result: the params contain raw
// arguments, no input validation is performed, and the result is returned to
// the user as-is, without any validation of the output.
//
// Most users will write a [ToolHandlerFor] and install it with the generic
// [AddTool] function.
//
// If ToolHandler returns an error, it is treated as a protocol error. By
// contrast, [ToolHandlerFor] automatically populates [CallToolResult.IsError]
// and [CallToolResult.Content] accordingly.
type ToolHandler func(context.Context, *CallToolRequest) (*CallToolResult, error)

// A ToolHandlerFor handles a call to tools/call with typed arguments and results.
//
// Use [AddTool] to add a ToolHandlerFor to a server.
//
// Unlike [ToolHandler], [ToolHandlerFor] provides significant functionality
// out of the box, and enforces that the tool conforms to the MCP spec:
//   - The In type provides a default input schema for the tool, though it may
//     be overridden in [AddTool].
//   - The input value is automatically unmarshaled from req.Params.Arguments.
//   - The input value is automatically validated against its input schema.
//     Invalid input is rejected before getting to the handler.
//   - If the Out type is not the empty interface [any], it provides the
//     default output schema for the tool (which again may be overridden in
//     [AddTool]).
//   - The Out value is used to populate result.StructuredContent.
//   - If [CallToolResult.Content] is unset, it is populated with the JSON
//     content of the output.
//   - An error result is treated as a tool error, rather than a protocol
//     error, and is therefore packed into CallToolResult.Content, with
//     [IsError] set.
//
// For these reasons, most users can ignore the [CallToolRequest] argument and
// [CallToolResult] return values entirely. In fact, it is permissible to
// return a nil CallToolResult, if you only care about returning a output value
// or error. The effective result will be populated as described above.
type ToolHandlerFor[In, Out any] func(_ context.Context, request *CallToolRequest, input In) (result *CallToolResult, output Out, _ error)

// A serverTool is a tool definition that is bound to a tool handler.
type serverTool struct {
	tool    *Tool
	handler ToolHandler
}

// applySchema validates whether data is valid JSON according to the provided
// schema, after applying schema defaults.
//
// If forOutput is false, the data is treated as tool input: the schema's root
// type must be "object" and the value is unmarshaled into a map.
//
// If forOutput is true, the data is treated as tool output: the schema's root
// may be of any type (object, array, primitive, composition).
//
// Returns the JSON value, augmented with defaults where applicable.
func applySchema(data json.RawMessage, resolved *jsonschema.Resolved, forOutput bool) (json.RawMessage, error) {
	// TODO: use reflection to create the struct type to unmarshal into.
	// Separate validation from assignment.

	// Use default JSON marshalling for validation.
	//
	// This avoids inconsistent representation due to custom marshallers, such as
	// time.Time (issue #449).
	//
	// For input, unmarshalling into a map ensures that the resulting JSON is
	// at least {}, even if data is empty. For example, arguments is technically
	// an optional property of callToolParams, and we still want to apply the
	// defaults in this case.
	//
	// TODO(rfindley): in which cases can resolved be nil?
	if resolved == nil {
		return data, nil
	}

	// Decode numbers as json.Number so that any re-marshalling below
	// reproduces their original literal text. Decoding into any/map[string]any
	// otherwise represents every JSON number as a float64, which silently
	// rounds integers outside the IEEE-754 safe range (issue #1201).
	var unmarshaled any
	if !forOutput {
		v := make(map[string]any)
		if len(data) > 0 {
			if err := internaljson.UnmarshalUseNumber(data, &v); err != nil {
				return nil, fmt.Errorf("unmarshaling arguments: %w", err)
			}
		}
		unmarshaled = v
	} else {
		if len(data) > 0 {
			if err := internaljson.UnmarshalUseNumber(data, &unmarshaled); err != nil {
				return nil, fmt.Errorf("unmarshaling output: %w", err)
			}
		}
	}

	// Apply defaults only when the value is a map: jsonschema.Resolved.ApplyDefaults
	// only operates on object properties. For object-rooted output schemas,
	// coerce a nil result (from "null" or empty data) into {} so handlers that
	// return a typed-nil map still validate.
	appliedDefaults := false
	if _, ok := unmarshaled.(map[string]any); ok {
		if err := resolved.ApplyDefaults(&unmarshaled); err != nil {
			return nil, fmt.Errorf("applying schema defaults:\n%w", err)
		}
		appliedDefaults = true
	} else if forOutput && unmarshaled == nil && resolved.Schema().Type == "object" {
		unmarshaled = make(map[string]any)
		if err := resolved.ApplyDefaults(&unmarshaled); err != nil {
			return nil, fmt.Errorf("applying schema defaults:\n%w", err)
		}
		appliedDefaults = true
	}

	// Validate a float64 copy: a json.Number has reflect.Kind String, so
	// jsonschema reports it as a JSON string and rejects it against
	// "type": "integer". Converting keeps validation on exactly the
	// representation it has always seen.
	forValidation, err := jsonNumbersAsFloat(unmarshaled)
	if err != nil {
		return nil, err
	}
	if err := resolved.Validate(&forValidation); err != nil {
		return nil, err
	}

	// Re-marshal only when defaults may have changed the value.
	if !appliedDefaults {
		return data, nil
	}
	out, err := json.Marshal(unmarshaled)
	if err != nil {
		return nil, fmt.Errorf("marshalling with defaults: %v", err)
	}
	return out, nil
}

// jsonNumbersAsFloat returns a copy of v with every [json.Number] replaced by
// the float64 it denotes, leaving all other values alone.
//
// It reports an error for a number that cannot be represented as a float64,
// matching the error that decoding it without [json.Decoder.UseNumber] would
// have produced.
func jsonNumbersAsFloat(v any) (any, error) {
	switch v := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for key, elem := range v {
			c, err := jsonNumbersAsFloat(elem)
			if err != nil {
				return nil, err
			}
			m[key] = c
		}
		return m, nil
	case []any:
		s := make([]any, len(v))
		for i, elem := range v {
			c, err := jsonNumbersAsFloat(elem)
			if err != nil {
				return nil, err
			}
			s[i] = c
		}
		return s, nil
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil, fmt.Errorf("number %s cannot be unmarshaled into a float64", v)
		}
		return f, nil
	default:
		return v, nil
	}
}

// isObjectJSON reports whether data is a JSON object (i.e., starts with '{'
// after any leading whitespace). Returns false for arrays, primitives, null,
// or empty input.
func isObjectJSON(data json.RawMessage) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// validateToolName checks whether name is a valid tool name, reporting a
// non-nil error if not.
func validateToolName(name string) error {
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("tool name exceeds maximum length of 128 characters (current: %d)", len(name))
	}
	// For consistency with other SDKs, report characters in the order the appear
	// in the name.
	var invalidChars []string
	seen := make(map[rune]bool)
	for _, r := range name {
		if !validToolNameRune(r) {
			if !seen[r] {
				invalidChars = append(invalidChars, fmt.Sprintf("%q", string(r)))
				seen[r] = true
			}
		}
	}
	if len(invalidChars) > 0 {
		return fmt.Errorf("tool name contains invalid characters: %s", strings.Join(invalidChars, ", "))
	}
	return nil
}

// validToolNameRune reports whether r is valid within tool names.
func validToolNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-' || r == '.'
}
