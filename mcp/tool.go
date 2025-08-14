// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// A ToolHandler handles a call to tools/call.
// req.Params.Arguments will contain a json.RawMessage containing the arguments.
// args will contain a value that has been validated against the input schema.
type ToolHandler func(ctx context.Context, req *ServerRequest[*CallToolParams], args any) (*CallToolResult, error)

type CallToolRequest struct {
	Session *ServerSession
	Params  *CallToolParams
}

type rawToolHandler func(ctx context.Context, req *ServerRequest[*CallToolParams]) (*CallToolResult, error)

// A serverTool is a tool definition that is bound to a tool handler.
type serverTool struct {
	tool    *Tool
	handler rawToolHandler
	// Resolved tool schemas. Set in newServerTool.
	inputResolved, outputResolved *jsonschema.Resolved
}

// A TypedToolHandler handles a call to tools/call with typed arguments and results.
type TypedToolHandler[In, Out any] func(context.Context, *ServerRequest[*CallToolParams], In) (*CallToolResult, Out, error)

func newServerTool(t *Tool, h ToolHandler) (*serverTool, error) {
	st := &serverTool{tool: t}
	if t.newArgs == nil {
		t.newArgs = func() any { return &map[string]any{} }
	}
	if t.InputSchema == nil {
		// This prevents the tool author from forgetting to write a schema where
		// one should be provided. If we papered over this by supplying the empty
		// schema, then every input would be validated and the problem wouldn't be
		// discovered until runtime, when the LLM sent bad data.
		return nil, errors.New("missing input schema")
	}
	var err error
	st.inputResolved, err = t.InputSchema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	if t.OutputSchema != nil {
		st.outputResolved, err = t.OutputSchema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	}
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}
	// Ignore output schema.
	st.handler = func(ctx context.Context, req *ServerRequest[*CallToolParams]) (*CallToolResult, error) {
		rawArgs := req.Params.Arguments.(json.RawMessage)
		args := t.newArgs()
		if err := unmarshalSchema(rawArgs, st.inputResolved, args); err != nil {
			return nil, err
		}
		res, err := h(ctx, req, args)
		// TODO(rfindley): investigate why server errors are embedded in this strange way,
		// rather than returned as jsonrpc2 server errors.
		if err != nil {
			return &CallToolResult{
				Content: []Content{&TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}
		// TODO(jba): if t.OutputSchema != nil, check that StructuredContent is present and validates.
		return res, nil
	}
	return st, nil
}

// newTypedServerTool creates a serverTool from a tool and a handler.
// If the tool doesn't have an input schema, it is inferred from In.
// If the tool doesn't have an output schema and Out != any, it is inferred from Out.
func newTypedServerTool[In, Out any](t *Tool, h TypedToolHandler[In, Out]) (*serverTool, error) {
	assert(t.newArgs == nil, "newArgs is nil")
	t.newArgs = func() any { var x In; return &x }

	var err error
	t.InputSchema, err = jsonschema.For[In](nil)
	if err != nil {
		return nil, err
	}
	if reflect.TypeFor[Out]() != reflect.TypeFor[any]() {
		t.OutputSchema, err = jsonschema.For[Out](nil)
	}
	if err != nil {
		return nil, err
	}

	toolHandler := func(ctx context.Context, req *ServerRequest[*CallToolParams], args any) (*CallToolResult, error) {
		res, out, err := h(ctx, req, *args.(*In))
		if err != nil {
			return nil, err
		}
		if res == nil {
			res = &CallToolResult{}
		}
		// TODO: return the serialized JSON in a TextContent block, as per spec?
		// https://modelcontextprotocol.io/specification/2025-06-18/server/tools#structured-content
		res.StructuredContent = out
		return res, nil
	}
	return newServerTool(t, toolHandler)
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
