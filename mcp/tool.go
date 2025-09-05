// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
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
//   - The Out value is used to populate result.StructuredOutput.
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

// unmarshalSchema unmarshals data into v and validates the result according to
// the given resolved schema.
func unmarshalSchema(data json.RawMessage, resolved *jsonschema.Resolved, v any) error {
	// TODO: use reflection to create the struct type to unmarshal into.
	// Separate validation from assignment.

	// Disallow unknown fields.
	// Otherwise, if the tool was built with a struct, the client could send extra
	// fields and json.Unmarshal would ignore them, so the schema would never get
	// a chance to declare the extra args invalid.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("unmarshaling: %w", err)
	}
	return validateSchema(resolved, v)
}

func validateSchema(resolved *jsonschema.Resolved, value any) error {
	if resolved != nil {
		if err := resolved.ApplyDefaults(value); err != nil {
			return fmt.Errorf("applying defaults from \n\t%s\nto\n\t%v:\n%w", schemaJSON(resolved.Schema()), value, err)
		}
		if err := resolved.Validate(value); err != nil {
			return fmt.Errorf("validating\n\t%v\nagainst\n\t %s:\n %w", value, schemaJSON(resolved.Schema()), err)
		}
	}
	return nil
}

// schemaJSON returns the JSON value for s as a string, or a string indicating an error.
func schemaJSON(s *jsonschema.Schema) string {
	m, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("<!%s>", err)
	}
	return string(m)
}
