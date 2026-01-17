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
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

type hiParams struct {
	Name string
}

// TODO(jba): after schemas are stateless (WIP), this can be a variable.
func greetTool() *Tool { return &Tool{Name: "greet", Description: "say hi"} }

func sayHi(ctx context.Context, req *CallToolRequest, args hiParams) (*CallToolResult, any, error) {
	if err := req.Session.Ping(ctx, nil); err != nil {
		return nil, nil, fmt.Errorf("ping failed: %v", err)
	}
	return &CallToolResult{Content: []Content{&TextContent{Text: "hi " + args.Name}}}, nil, nil
}

var codeReviewPrompt = &Prompt{
	Name:        "code_review",
	Description: "do a code review",
	Arguments:   []*PromptArgument{{Name: "Code", Required: true}},
}

func codReviewPromptHandler(_ context.Context, req *GetPromptRequest) (*GetPromptResult, error) {
	return &GetPromptResult{
		Description: "Code review prompt",
		Messages: []*PromptMessage{
			{Role: "user", Content: &TextContent{Text: "Please review the following code: " + req.Params.Arguments["Code"]}},
		},
	}, nil
}

// Registry of values to be referenced in tests.
var (
	errTestFailure = errors.New("mcp failure")

	resource1 = &Resource{
		Name:     "public",
		MIMEType: "text/plain",
		URI:      "file:///info.txt",
	}
	resource2 = &Resource{
		Name:     "public", // names are not unique IDs
		MIMEType: "text/plain",
		URI:      "file:///fail.txt",
	}
	resource3 = &Resource{
		Name:     "info",
		MIMEType: "text/plain",
		URI:      "embedded:info",
	}
	readHandler = fileResourceHandler("testdata/files")
)

var embeddedResources = map[string]string{
	"info": "This is the MCP test server.",
}

func handleEmbeddedResource(_ context.Context, req *ReadResourceRequest) (*ReadResourceResult, error) {
	u, err := url.Parse(req.Params.URI)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "embedded" {
		return nil, fmt.Errorf("wrong scheme: %q", u.Scheme)
	}
	key := u.Opaque
	text, ok := embeddedResources[key]
	if !ok {
		return nil, fmt.Errorf("no embedded resource named %q", key)
	}
	return &ReadResourceResult{
		Contents: []*ResourceContents{
			{URI: req.Params.URI, MIMEType: "text/plain", Text: text},
		},
	}, nil
}

// errorCode returns the code associated with err.
// If err is nil, it returns 0.
// If there is no code, it returns -1.
func errorCode(err error) int64 {
	if err == nil {
		return 0
	}
	var werr *jsonrpc.Error
	if errors.As(err, &werr) {
		return werr.Code
	}
	return -1
}

// basicConnection returns a new basic client-server connection, with the server
// configured via the provided function.
//
// The caller should cancel either the client connection or server connection
// when the connections are no longer needed.
//
// The returned func cleans up by closing the client and waiting for the server
// to shut down.
func basicConnection(t *testing.T, config func(*Server)) (*ClientSession, *ServerSession, func()) {
	return basicClientServerConnection(t, nil, nil, config)
}

// basicClientServerConnection creates a basic connection between client and
// server. If either client or server is nil, empty implementations are used.
//
// The provided function may be used to configure features on the resulting
// server, prior to connection.
//
// The caller should cancel either the client connection or server connection
// when the connections are no longer needed.
//
// The returned func cleans up by closing the client and waiting for the server
// to shut down.
func basicClientServerConnection(t *testing.T, client *Client, server *Server, config func(*Server)) (*ClientSession, *ServerSession, func()) {
	t.Helper()

	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	if server == nil {
		server = NewServer(testImpl, nil)
	}
	if config != nil {
		config(server)
	}
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	if client == nil {
		client = NewClient(testImpl, nil)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return cs, ss, func() {
		cs.Close()
		ss.Wait()
	}
}

func TestServerClosing(t *testing.T) {
	cs, ss, cleanup := basicConnection(t, func(s *Server) {
		AddTool(s, greetTool(), sayHi)
	})
	defer cleanup()

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		if err := cs.Wait(); err != nil {
			t.Errorf("server connection failed: %v", err)
		}
		wg.Done()
	}()
	if _, err := cs.CallTool(ctx, &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"Name": "user"},
	}); err != nil {
		t.Fatalf("after connecting: %v", err)
	}
	ss.Close()
	wg.Wait()
	if _, err := cs.CallTool(ctx, &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "user"},
	}); !errors.Is(err, ErrConnectionClosed) {
		t.Errorf("after disconnection, got error %v, want EOF", err)
	}
}

func TestMiddleware(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the server to exit after the client closes its connection.
	t.Cleanup(func() { _ = ss.Close() })

	var sbuf, cbuf bytes.Buffer
	sbuf.WriteByte('\n')
	cbuf.WriteByte('\n')

	// "1" is the outer middleware layer, called first; then "2" is called, and finally
	// the default dispatcher.
	s.AddSendingMiddleware(traceCalls[*ServerSession](&sbuf, "S1"), traceCalls[*ServerSession](&sbuf, "S2"))
	s.AddReceivingMiddleware(traceCalls[*ServerSession](&sbuf, "R1"), traceCalls[*ServerSession](&sbuf, "R2"))

	c := NewClient(testImpl, nil)
	c.AddSendingMiddleware(traceCalls[*ClientSession](&cbuf, "S1"), traceCalls[*ClientSession](&cbuf, "S2"))
	c.AddReceivingMiddleware(traceCalls[*ClientSession](&cbuf, "R1"), traceCalls[*ClientSession](&cbuf, "R2"))

	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.ListRoots(ctx, nil); err != nil {
		t.Fatal(err)
	}

	wantServer := `
R1 >initialize
R2 >initialize
R2 <initialize
R1 <initialize
R1 >notifications/initialized
R2 >notifications/initialized
R2 <notifications/initialized
R1 <notifications/initialized
R1 >tools/list
R2 >tools/list
R2 <tools/list
R1 <tools/list
S1 >roots/list
S2 >roots/list
S2 <roots/list
S1 <roots/list
`
	if diff := cmp.Diff(wantServer, sbuf.String()); diff != "" {
		t.Errorf("server mismatch (-want, +got):\n%s", diff)
	}

	wantClient := `
S1 >initialize
S2 >initialize
S2 <initialize
S1 <initialize
S1 >notifications/initialized
S2 >notifications/initialized
S2 <notifications/initialized
S1 <notifications/initialized
S1 >tools/list
S2 >tools/list
S2 <tools/list
S1 <tools/list
R1 >roots/list
R2 >roots/list
R2 <roots/list
R1 <roots/list
`
	if diff := cmp.Diff(wantClient, cbuf.String()); diff != "" {
		t.Errorf("client mismatch (-want, +got):\n%s", diff)
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func TestNoJSONNull(t *testing.T) {
	ctx := context.Background()
	var ct, st Transport = NewInMemoryTransports()

	// Collect logs, to sanity check that we don't write JSON null anywhere.
	var logbuf safeBuffer
	ct = &LoggingTransport{Transport: ct, Writer: &logbuf}

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	c := NewClient(testImpl, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListPrompts(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResources(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListResourceTemplates(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.ListRoots(ctx, nil); err != nil {
		t.Fatal(err)
	}

	cs.Close()
	ss.Wait()

	logs := logbuf.Bytes()
	if i := bytes.Index(logs, []byte("null")); i >= 0 {
		start := max(i-20, 0)
		end := min(i+20, len(logs))
		t.Errorf("conformance violation: MCP logs contain JSON null: %s", "..."+string(logs[start:end])+"...")
	}
}

// traceCalls creates a middleware function that prints the method before and after each call
// with the given prefix.
func traceCalls[S Session](w io.Writer, prefix string) Middleware {
	return func(h MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			fmt.Fprintf(w, "%s >%s\n", prefix, method)
			defer fmt.Fprintf(w, "%s <%s\n", prefix, method)
			return h(ctx, method, req)
		}
	}
}

func nopHandler(context.Context, *CallToolRequest) (*CallToolResult, error) {
	return nil, nil
}

func TestElicitationUnsupportedMethod(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	// Server
	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	// Client without ElicitationHandler
	c := NewClient(testImpl, &ClientOptions{
		CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
			return &CreateMessageResult{Model: "aModel", Content: &TextContent{}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Test that elicitation fails when no handler is provided
	_, err = ss.Elicit(ctx, &ElicitParams{
		Message: "This should fail",
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"test": {Type: "string"},
			},
		},
	})

	if err == nil {
		t.Error("expected error when ElicitationHandler is not provided, got nil")
	}
	if code := errorCode(err); code != -1 {
		t.Errorf("got error code %d, want -1", code)
	}
	if !strings.Contains(err.Error(), "does not support elicitation") {
		t.Errorf("error should mention unsupported elicitation, got: %v", err)
	}
}

func anyPtr[T any](v T) *any {
	var a any = v
	return &a
}

func TestElicitationSchemaValidation(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept", Content: map[string]any{"test": "value"}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// Test valid schemas - these should not return errors
	validSchemas := []struct {
		name   string
		schema *jsonschema.Schema
	}{
		{
			name:   "nil schema",
			schema: nil,
		},
		{
			name: "empty object schema",
			schema: &jsonschema.Schema{
				Type: "object",
			},
		},
		{
			name: "simple string property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
			},
		},
		{
			name: "string with valid formats",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"email":    {Type: "string", Format: "email"},
					"website":  {Type: "string", Format: "uri"},
					"birthday": {Type: "string", Format: "date"},
					"created":  {Type: "string", Format: "date-time"},
				},
			},
		},
		{
			name: "string with constraints",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MinLength: ptr(1), MaxLength: ptr(100)},
				},
			},
		},
		{
			name: "number with constraints",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"age":   {Type: "integer", Minimum: ptr(0.0), Maximum: ptr(150.0)},
					"score": {Type: "number", Minimum: ptr(0.0), Maximum: ptr(100.0)},
				},
			},
		},
		{
			name: "boolean with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "boolean", Default: json.RawMessage("true")},
				},
			},
		},
		{
			name: "string enum",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"status": {
						Type: "string",
						Enum: []any{
							"active",
							"inactive",
							"pending",
						},
					},
				},
			},
		},
		{
			name: "enum with matching enumNames",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Type: "string",
						Enum: []any{
							"high",
							"medium",
							"low",
						},
						Extra: map[string]any{
							"enumNames": []any{"High Priority", "Medium Priority", "Low Priority"},
						},
					},
				},
			},
		},
		{
			name: "enum with enum schema",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Type: "string",
						OneOf: []*jsonschema.Schema{
							{
								Const: anyPtr(map[string]string{
									"const": "high",
									"title": "High Priority",
								}),
							},
							{
								Const: anyPtr(map[string]string{
									"const": "medium",
									"title": "Medium Priority",
								}),
							},
							{
								Const: anyPtr(map[string]string{
									"const": "low",
									"title": "Low Priority",
								}),
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range validSchemas {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			_, err := ss.Elicit(ctx, &ElicitParams{
				Message:         "Test valid schema: " + tc.name,
				RequestedSchema: tc.schema,
			})
			if err != nil {
				t.Errorf("expected no error for valid schema %q, got: %v", tc.name, err)
			}
		})
	}

	// Test invalid schemas - these should return errors
	invalidSchemas := []struct {
		name          string
		schema        *jsonschema.Schema
		expectedError string
	}{
		{
			name: "root schema non-object type",
			schema: &jsonschema.Schema{
				Type: "string",
			},
			expectedError: "elicit schema must be of type 'object', got \"string\"",
		},
		{
			name: "nested object property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"user": {
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"name": {Type: "string"},
						},
					},
				},
			},
			expectedError: "elicit schema property \"user\" contains nested properties, only primitive properties are allowed",
		},
		{
			name: "property with explicit object type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"config": {Type: "object"},
				},
			},
			expectedError: "elicit schema property \"config\" has unsupported type \"object\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "array property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"tags": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
				},
			},
			expectedError: "elicit schema property \"tags\" has unsupported type \"array\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "array without items",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"items": {Type: "array"},
				},
			},
			expectedError: "elicit schema property \"items\" has unsupported type \"array\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "unsupported string format",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"phone": {Type: "string", Format: "phone"},
				},
			},
			expectedError: "elicit schema property \"phone\" has unsupported format \"phone\", only email, uri, date, and date-time are allowed",
		},
		{
			name: "unsupported type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"data": {Type: "null"},
				},
			},
			expectedError: "elicit schema property \"data\" has unsupported type \"null\"",
		},
		{
			name: "string with invalid minLength",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MinLength: ptr(-1)},
				},
			},
			expectedError: "elicit schema property \"name\" has invalid minLength -1, must be non-negative",
		},
		{
			name: "string with invalid maxLength",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MaxLength: ptr(-5)},
				},
			},
			expectedError: "elicit schema property \"name\" has invalid maxLength -5, must be non-negative",
		},
		{
			name: "string with maxLength less than minLength",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string", MinLength: ptr(10), MaxLength: ptr(5)},
				},
			},
			expectedError: "elicit schema property \"name\" has maxLength 5 less than minLength 10",
		},
		{
			name: "number with maximum less than minimum",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"score": {Type: "number", Minimum: ptr(100.0), Maximum: ptr(50.0)},
				},
			},
			expectedError: "elicit schema property \"score\" has maximum 50 less than minimum 100",
		},
		{
			name: "boolean with invalid default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "boolean", Default: json.RawMessage(`"not-a-boolean"`)},
				},
			},
			expectedError: "elicit schema property \"enabled\" has invalid default value, must be a bool",
		},
		{
			name: "string with invalid default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "string", Default: json.RawMessage("true")},
				},
			},
			expectedError: "elicit schema property \"enabled\" has invalid default value, must be a string",
		},
		{
			name: "integer with invalid default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "integer", Default: json.RawMessage("true")},
				},
			},
			expectedError: "elicit schema property \"enabled\" has default value that cannot be interpreted as an int or float",
		},
		{
			name: "number with invalid default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"enabled": {Type: "number", Default: json.RawMessage("true")},
				},
			},
			expectedError: "elicit schema property \"enabled\" has default value that cannot be interpreted as an int or float",
		},
		{
			name: "enum with mismatched enumNames length",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Type: "string",
						Enum: []any{
							"high",
							"medium",
							"low",
						},
						Extra: map[string]any{
							"enumNames": []any{"High Priority", "Medium Priority"}, // Only 2 names for 3 values
						},
					},
				},
			},
			expectedError: "elicit schema property \"priority\" has 3 enum values but 2 enumNames, they must match",
		},
		{
			name: "enum with invalid enumNames type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"status": {
						Type: "string",
						Enum: []any{
							"active",
							"inactive",
						},
						Extra: map[string]any{
							"enumNames": "not an array", // Should be array
						},
					},
				},
			},
			expectedError: "elicit schema property \"status\" has invalid enumNames type, must be an array",
		},
		{
			name: "enum without explicit type",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"priority": {
						Enum: []any{
							"high",
							"medium",
							"low",
						},
					},
				},
			},
			expectedError: "elicit schema property \"priority\" has unsupported type \"\", only string, number, integer, and boolean are allowed",
		},
		{
			name: "untyped property",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"data": {},
				},
			},
			expectedError: "elicit schema property \"data\" has unsupported type \"\", only string, number, integer, and boolean are allowed",
		},
	}

	for _, tc := range invalidSchemas {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			_, err := ss.Elicit(ctx, &ElicitParams{
				Message:         "Test invalid schema: " + tc.name,
				RequestedSchema: tc.schema,
			})
			if err == nil {
				t.Errorf("expected error for invalid schema %q, got nil", tc.name)
				return
			}
			if code := errorCode(err); code != jsonrpc.CodeInvalidParams {
				t.Errorf("got error code %d, want %d (CodeInvalidParams)", code, jsonrpc.CodeInvalidParams)
			}
			if !strings.Contains(err.Error(), tc.expectedError) {
				t.Errorf("error message %q does not contain expected text %q", err.Error(), tc.expectedError)
			}
		})
	}
}

func TestElicitContentValidation(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	// Set up a client that exercises valid/invalid elicitation: the returned
	// Content from the handler ("potato") is validated against the schemas
	// defined in the testcases below.
	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept", Content: map[string]any{"test": "potato"}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	testcases := []struct {
		name          string
		schema        *jsonschema.Schema
		expectedError string
	}{
		{
			name: "string enum with schema not matching content",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"test": {
						Type: "string",
						OneOf: []*jsonschema.Schema{
							{
								Const: anyPtr(map[string]string{
									"const": "high",
									"title": "High Priority",
								}),
							},
						},
					},
				},
			},
			expectedError: "oneOf: did not validate against any of",
		},
		{
			name: "string enum with schema matching content",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"test": {
						Type: "string",
						OneOf: []*jsonschema.Schema{
							{
								Const: anyPtr(map[string]string{
									"const": "potato",
									"title": "Potato Priority",
								}),
							},
						},
					},
				},
			},
			expectedError: "",
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ss.Elicit(ctx, &ElicitParams{
				Message:         "Test schema: " + tc.name,
				RequestedSchema: tc.schema,
			})
			if tc.expectedError != "" {
				if err == nil {
					t.Errorf("expected error but got no error: %s", tc.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("error message %q does not contain expected text %q", err.Error(), tc.expectedError)
				}
			}
		})
	}
}

func TestElicitationProgressToken(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept"}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	params := &ElicitParams{
		Message: "Test progress token",
		Meta:    Meta{},
	}
	params.SetProgressToken("test-token")

	if token := params.GetProgressToken(); token != "test-token" {
		t.Errorf("got progress token %v, want %q", token, "test-token")
	}

	_, err = ss.Elicit(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
}

func TestElicitationCapabilityDeclaration(t *testing.T) {
	ctx := context.Background()

	t.Run("with handler", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		// Client with ElicitationHandler should declare capability
		c := NewClient(testImpl, &ClientOptions{
			ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
				return &ElicitResult{Action: "cancel"}, nil
			},
		})

		s := NewServer(testImpl, nil)
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// The client should have declared elicitation capability during initialization
		// We can verify this worked by successfully making an elicitation call
		result, err := ss.Elicit(ctx, &ElicitParams{
			Message:         "Test capability",
			RequestedSchema: &jsonschema.Schema{Type: "object"},
		})
		if err != nil {
			t.Fatalf("elicitation should work when capability is declared, got error: %v", err)
		}
		if result.Action != "cancel" {
			t.Errorf("got action %q, want %q", result.Action, "cancel")
		}
	})

	t.Run("without handler", func(t *testing.T) {
		ct, st := NewInMemoryTransports()

		// Client without ElicitationHandler should not declare capability
		c := NewClient(testImpl, &ClientOptions{
			CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
				return &CreateMessageResult{Model: "aModel", Content: &TextContent{}}, nil
			},
		})

		s := NewServer(testImpl, nil)
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()

		cs, err := c.Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()

		// Elicitation should fail with UnsupportedMethod
		_, err = ss.Elicit(ctx, &ElicitParams{
			Message:         "This should fail",
			RequestedSchema: &jsonschema.Schema{Type: "object"},
		})

		if err == nil {
			t.Error("expected UnsupportedMethod error when no capability declared")
		}
		if code := errorCode(err); code != -1 {
			t.Errorf("got error code %d, want -1", code)
		}
	})
}

func TestElicitationDefaultValues(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	c := NewClient(testImpl, &ClientOptions{
		ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
			return &ElicitResult{Action: "accept", Content: map[string]any{"default": "response"}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	testcases := []struct {
		name     string
		schema   *jsonschema.Schema
		expected map[string]any
	}{
		{
			name: "boolean with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"key": {Type: "boolean", Default: json.RawMessage("true")},
				},
			},
			expected: map[string]any{"key": true, "default": "response"},
		},
		{
			name: "string with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"key": {Type: "string", Default: json.RawMessage("\"potato\"")},
				},
			},
			expected: map[string]any{"key": "potato", "default": "response"},
		},
		{
			name: "integer with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"key": {Type: "integer", Default: json.RawMessage("123")},
				},
			},
			expected: map[string]any{"key": float64(123), "default": "response"},
		},
		{
			name: "number with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"key": {Type: "number", Default: json.RawMessage("89.7")},
				},
			},
			expected: map[string]any{"key": float64(89.7), "default": "response"},
		},
		{
			name: "enum with default",
			schema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"key": {Type: "string", Enum: []any{"one", "two"}, Default: json.RawMessage("\"one\"")},
				},
			},
			expected: map[string]any{"key": "one", "default": "response"},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ss.Elicit(ctx, &ElicitParams{
				Message:         "Test schema with defaults: " + tc.name,
				RequestedSchema: tc.schema,
			})
			if err != nil {
				t.Fatalf("expected no error for default schema %q, got: %v", tc.name, err)
			}
			if diff := cmp.Diff(tc.expected, res.Content); diff != "" {
				t.Errorf("%s: did not get expected value, -want +got:\n%s", tc.name, diff)
			}
		})
	}
}

func TestAddTool_DuplicateNoPanicAndNoDuplicate(t *testing.T) {
	// Adding the same tool pointer twice should not panic and should not
	// produce duplicates in the server's tool list.
	cs, _, cleanup := basicConnection(t, func(s *Server) {
		// Use two distinct Tool instances with the same name but different
		// descriptions to ensure the second replaces the first
		// This case was written specifically to reproduce a bug where duplicate tools where causing jsonschema errors
		t1 := &Tool{Name: "dup", Description: "first", InputSchema: &jsonschema.Schema{Type: "object"}}
		t2 := &Tool{Name: "dup", Description: "second", InputSchema: &jsonschema.Schema{Type: "object"}}
		s.AddTool(t1, nopHandler)
		s.AddTool(t2, nopHandler)
	})
	defer cleanup()

	ctx := context.Background()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var gotDesc string
	for _, tt := range res.Tools {
		if tt.Name == "dup" {
			count++
			gotDesc = tt.Description
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one 'dup' tool, got %d", count)
	}
	if gotDesc != "second" {
		t.Fatalf("expected replaced tool to have description %q, got %q", "second", gotDesc)
	}
}

var testImpl = &Implementation{Name: "test", Version: "v1.0.0"}

// This test checks that when we use pointer types for tools, we get the same
// schema as when using the non-pointer types. It is too much of a footgun for
// there to be a difference (see #199 and #200).
//
// If anyone asks, we can add an option that controls how pointers are treated.
func TestPointerArgEquivalence(t *testing.T) {
	type input struct {
		In string `json:",omitempty"`
	}
	type output struct {
		Out string
	}
	cs, _, cleanup := basicConnection(t, func(s *Server) {
		// Add two equivalent tools, one of which operates in the 'pointer' realm,
		// the other of which does not.
		//
		// We handle a few different types of results, to assert they behave the
		// same in all cases.
		AddTool(s, &Tool{Name: "pointer"}, func(_ context.Context, req *CallToolRequest, in *input) (*CallToolResult, *output, error) {
			switch in.In {
			case "":
				return nil, nil, fmt.Errorf("must provide input")
			case "nil":
				return nil, nil, nil
			case "empty":
				return &CallToolResult{}, nil, nil
			case "ok":
				return &CallToolResult{}, &output{Out: "foo"}, nil
			default:
				panic("unreachable")
			}
		})
		AddTool(s, &Tool{Name: "nonpointer"}, func(_ context.Context, req *CallToolRequest, in input) (*CallToolResult, output, error) {
			switch in.In {
			case "":
				return nil, output{}, fmt.Errorf("must provide input")
			case "nil":
				return nil, output{}, nil
			case "empty":
				return &CallToolResult{}, output{}, nil
			case "ok":
				return &CallToolResult{}, output{Out: "foo"}, nil
			default:
				panic("unreachable")
			}
		})
	})
	defer cleanup()

	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(tools.Tools), 2; got != want {
		t.Fatalf("got %d tools, want %d", got, want)
	}
	t0 := tools.Tools[0]
	t1 := tools.Tools[1]

	// First, check that the tool schemas don't differ.
	if diff := cmp.Diff(t0.InputSchema, t1.InputSchema); diff != "" {
		t.Errorf("input schemas do not match (-%s +%s):\n%s", t0.Name, t1.Name, diff)
	}
	if diff := cmp.Diff(t0.OutputSchema, t1.OutputSchema); diff != "" {
		t.Errorf("output schemas do not match (-%s +%s):\n%s", t0.Name, t1.Name, diff)
	}

	// Then, check that we handle empty input equivalently.
	for _, args := range []any{nil, struct{}{}} {
		r0, err := cs.CallTool(ctx, &CallToolParams{Name: t0.Name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		r1, err := cs.CallTool(ctx, &CallToolParams{Name: t1.Name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(r0, r1, ctrCmpOpts...); diff != "" {
			t.Errorf("CallTool(%v) with no arguments mismatch (-%s +%s):\n%s", args, t0.Name, t1.Name, diff)
		}
	}

	// Then, check that we handle different types of output equivalently.
	for _, in := range []string{"nil", "empty", "ok"} {
		t.Run(in, func(t *testing.T) {
			r0, err := cs.CallTool(ctx, &CallToolParams{Name: t0.Name, Arguments: input{In: in}})
			if err != nil {
				t.Fatal(err)
			}
			r1, err := cs.CallTool(ctx, &CallToolParams{Name: t1.Name, Arguments: input{In: in}})
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(r0, r1, ctrCmpOpts...); diff != "" {
				t.Errorf("CallTool({\"In\": %q}) mismatch (-%s +%s):\n%s", in, t0.Name, t1.Name, diff)
			}
		})
	}
}

// ptr is a helper function to create pointers for schema constraints
func ptr[T any](v T) *T {
	return &v
}

func TestComplete(t *testing.T) {
	completionValues := []string{"python", "pytorch", "pyside"}

	serverOpts := &ServerOptions{
		CompletionHandler: func(_ context.Context, request *CompleteRequest) (*CompleteResult, error) {
			return &CompleteResult{
				Completion: CompletionResultDetails{
					Values: completionValues,
				},
			}, nil
		},
	}
	server := NewServer(testImpl, serverOpts)
	cs, _, cleanup := basicClientServerConnection(t, nil, server, func(s *Server) {})
	defer cleanup()

	result, err := cs.Complete(context.Background(), &CompleteParams{
		Argument: CompleteParamsArgument{
			Name:  "language",
			Value: "py",
		},
		Ref: &CompleteReference{
			Type: "ref/prompt",
			Name: "code_review",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(completionValues, result.Completion.Values); diff != "" {
		t.Errorf("Complete() mismatch (-want +got):\n%s", diff)
	}
}

// TestEmbeddedStructResponse performs a tool call to verify that a struct with
// an embedded pointer generates a correct, flattened JSON schema and that its
// response is validated successfully.
func TestEmbeddedStructResponse(t *testing.T) {
	type foo struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	// bar embeds foo
	type bar struct {
		*foo         // Embedded - should flatten in JSON
		Extra string `json:"extra"`
	}

	type response struct {
		Data bar `json:"data"`
	}

	// testTool demonstrates an embedded struct in its response.
	testTool := func(ctx context.Context, req *CallToolRequest, args any) (*CallToolResult, response, error) {
		response := response{
			Data: bar{
				foo: &foo{
					ID:   "foo",
					Name: "Test Foo",
				},
				Extra: "additional data",
			},
		}
		return nil, response, nil
	}
	ctx := context.Background()
	clientTransport, serverTransport := NewInMemoryTransports()
	server := NewServer(&Implementation{Name: "testServer", Version: "v1.0.0"}, nil)
	AddTool(server, &Tool{
		Name: "test_embedded_struct",
	}, testTool)

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	client := NewClient(&Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	_, err = clientSession.CallTool(ctx, &CallToolParams{
		Name: "test_embedded_struct",
	})
	if err != nil {
		t.Errorf("CallTool() failed: %v", err)
	}
}

func TestToolErrorMiddleware(t *testing.T) {
	ctx := context.Background()
	ct, st := NewInMemoryTransports()

	s := NewServer(testImpl, nil)
	AddTool(s, &Tool{
		Name:        "greet",
		Description: "say hi",
	}, sayHi)
	AddTool(s, &Tool{Name: "fail", InputSchema: &jsonschema.Schema{Type: "object"}},
		func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, any, error) {
			return nil, nil, errTestFailure
		})

	var middleErr error
	s.AddReceivingMiddleware(func(h MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			res, err := h(ctx, method, req)
			if err == nil {
				if ctr, ok := res.(*CallToolResult); ok {
					middleErr = ctr.getError()
				}
			}
			return res, err
		}
	})
	_, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(&Implementation{Name: "test-client"}, nil)
	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	_, err = clientSession.CallTool(ctx, &CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"Name": "al"},
	})
	if err != nil {
		t.Errorf("CallTool() failed: %v", err)
	}
	if middleErr != nil {
		t.Errorf("middleware got error %v, want nil", middleErr)
	}
	res, err := clientSession.CallTool(ctx, &CallToolParams{
		Name: "fail",
	})
	if err != nil {
		t.Errorf("CallTool() failed: %v", err)
	}
	if !res.IsError {
		t.Fatal("want error, got none")
	}
	// Clients can't see the error, because it isn't marshaled.
	if err := res.getError(); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if middleErr != errTestFailure {
		t.Errorf("middleware got err %v, want errTestFailure", middleErr)
	}
}

var ctrCmpOpts = []cmp.Option{cmp.AllowUnexported(CallToolResult{})}
