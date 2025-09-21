// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
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

// applySchema validates whether data is valid JSON according to the provided
// schema, after applying schema defaults.
//
// Returns the JSON value augmented with defaults.
func applySchema(data json.RawMessage, resolved *jsonschema.Resolved) (json.RawMessage, error) {
	if resolved != nil {
		validator := NewReflectionValidator()
		result, err := validator.ValidateAndApply(data, resolved)

		// If reflection-based validation succeeds, return the result
		if err == nil {
			return result, nil
		}

		// If reflection-based validation fails, fall back to map-based validation
		var schemaErr *SchemaValidationError
		if errors.As(err, &schemaErr) {
			if schemaErr.Operation == "schema_conversion" || schemaErr.Operation == "reflection_validation" {
				// Fall back to map-based validation for unsupported features or type mismatches
				return applySchemaMapBased(data, resolved)
			}
		}
		// For other types of errors, return them as-is
		return nil, err
	}

	return applySchemaMapBased(data, resolved)
}

// applySchemaMapBased performs schema validation using the original map-based approach.
// This is used as a fallback when reflection-based validation is not suitable.
func applySchemaMapBased(data json.RawMessage, resolved *jsonschema.Resolved) (json.RawMessage, error) {
	if resolved == nil {
		return data, nil
	}

	v := make(map[string]any)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("unmarshaling arguments: %w", err)
		}
	}
	if err := resolved.ApplyDefaults(&v); err != nil {
		return nil, fmt.Errorf("applying schema defaults:\n%w", err)
	}
	if err := resolved.Validate(&v); err != nil {
		return nil, err
	}
	// We must re-marshal with the default values applied.
	result, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshalling with defaults: %v", err)
	}
	return result, nil
}
