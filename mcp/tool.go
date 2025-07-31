// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
)

// A ToolHandler handles a call to tools/call.
// [CallToolParams.Arguments] will contain a map[string]any that has been validated
// against the input schema.
type ToolHandler = ToolHandlerFor[map[string]any, any]

// A ToolHandlerFor handles a call to tools/call with typed arguments and results.
type ToolHandlerFor[In, Out any] func(context.Context, *ServerSession, *CallToolParamsFor[In]) (*CallToolResultFor[Out], error)

// A rawToolHandler is like a ToolHandler, but takes the arguments as as json.RawMessage.
type rawToolHandler = func(context.Context, *ServerSession, *CallToolParamsFor[json.RawMessage]) (*CallToolResult, error)

// A serverTool is a tool definition that is bound to a tool handler.
type serverTool struct {
	tool    *Tool
	handler rawToolHandler
	// Resolved tool schemas. Set in newServerTool.
	inputResolved, outputResolved *jsonschema.Resolved
}

// newServerTool creates a serverTool from a tool and a handler.
// If the tool doesn't have an input schema, it is inferred from In.
// If the tool doesn't have an output schema and Out != any, it is inferred from Out.
func newServerTool[In, Out any](t *Tool, h ToolHandlerFor[In, Out]) (*serverTool, error) {
	st := &serverTool{tool: t}

	if err := setSchema[In](&t.InputSchema, &st.inputResolved); err != nil {
		return nil, err
	}
	if reflect.TypeFor[Out]() != reflect.TypeFor[any]() {
		if err := setSchema[Out](&t.OutputSchema, &st.outputResolved); err != nil {
			return nil, err
		}
	}

	st.handler = func(ctx context.Context, ss *ServerSession, rparams *CallToolParamsFor[json.RawMessage]) (*CallToolResult, error) {
		var args In
		if rparams.Arguments != nil {
			if err := unmarshalSchema(rparams.Arguments, st.inputResolved, &args); err != nil {
				return nil, err
			}
		}
		// TODO(jba): future-proof this copy.
		params := &CallToolParamsFor[In]{
			Meta:      rparams.Meta,
			Name:      rparams.Name,
			Arguments: args,
		}
		res, err := h(ctx, ss, params)
		// TODO(rfindley): investigate why server errors are embedded in this strange way,
		// rather than returned as jsonrpc2 server errors.
		if err != nil {
			return &CallToolResult{
				Content: []Content{&TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}
		var ctr CallToolResult
		// TODO(jba): What if res == nil? Is that valid?
		// TODO(jba): if t.OutputSchema != nil, check that StructuredContent is present and validates.
		if res != nil {
			// TODO(jba): future-proof this copy.
			ctr.Meta = res.Meta
			ctr.Content = res.Content
			ctr.IsError = res.IsError
			ctr.StructuredContent = res.StructuredContent
		}
		return &ctr, nil
	}

	return st, nil
}

func setSchema[T any](sfield **jsonschema.Schema, rfield **jsonschema.Resolved) error {
	var err error
	if *sfield == nil {
		*sfield, err = jsonschema.For[T](nil)
	}
	if err != nil {
		return err
	}
	*rfield, err = (*sfield).Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	return err
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
