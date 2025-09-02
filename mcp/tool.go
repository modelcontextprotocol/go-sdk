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
// [CallToolParams.Arguments] will contain a map[string]any that has been validated
// against the input schema.
type ToolHandler func(context.Context, *CallToolRequest) (*CallToolResult, error)

// A ToolHandlerFor handles a call to tools/call with typed arguments and results.
type ToolHandlerFor[In, Out any] func(context.Context, *CallToolRequest, In) (*CallToolResult, Out, error)

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
	// TODO: test with nil args.
	if resolved != nil {
		if err := resolved.ApplyDefaults(v); err != nil {
			return fmt.Errorf("applying defaults from \n\t%s\nto\n\t%s:\n%w", schemaJSON(resolved.Schema()), data, err)
		}
		if err := resolved.Validate(v); err != nil {
			return fmt.Errorf("validating\n\t%s\nagainst\n\t %s:\n %w", data, schemaJSON(resolved.Schema()), err)
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
