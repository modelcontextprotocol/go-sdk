// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/jsonschema-go/jsonschema"
)

type testItem struct {
	Name  string
	Value string
}

type testListParams struct {
	Cursor string
}

func (p *testListParams) cursorPtr() *string {
	return &p.Cursor
}

type testListResult struct {
	Items      []*testItem
	NextCursor string
}

func (r *testListResult) nextCursorPtr() *string {
	return &r.NextCursor
}

var allTestItems = []*testItem{
	{"alpha", "val-A"},
	{"bravo", "val-B"},
	{"charlie", "val-C"},
	{"delta", "val-D"},
	{"echo", "val-E"},
	{"foxtrot", "val-F"},
	{"golf", "val-G"},
	{"hotel", "val-H"},
	{"india", "val-I"},
	{"juliet", "val-J"},
	{"kilo", "val-K"},
}

// getCursor encodes a string input into a URL-safe base64 cursor,
// fatally logging any encoding errors.
func getCursor(input string) string {
	cursor, err := encodeCursor(input)
	if err != nil {
		log.Fatalf("encodeCursor(%s) error = %v", input, err)
	}
	return cursor
}

func TestServerPaginateBasic(t *testing.T) {
	testCases := []struct {
		name           string
		initialItems   []*testItem
		inputCursor    string
		inputPageSize  int
		wantFeatures   []*testItem
		wantNextCursor string
		wantErr        bool
	}{
		{
			name:           "FirstPage_DefaultSize_Full",
			initialItems:   allTestItems,
			inputCursor:    "",
			inputPageSize:  5,
			wantFeatures:   allTestItems[0:5],
			wantNextCursor: getCursor("echo"), // Based on last item of first page
			wantErr:        false,
		},
		{
			name:           "SecondPage_DefaultSize_Full",
			initialItems:   allTestItems,
			inputCursor:    getCursor("echo"),
			inputPageSize:  5,
			wantFeatures:   allTestItems[5:10],
			wantNextCursor: getCursor("juliet"), // Based on last item of second page
			wantErr:        false,
		},
		{
			name:           "SecondPage_DefaultSize_Full_OutOfOrder",
			initialItems:   append(allTestItems[5:], allTestItems[0:5]...),
			inputCursor:    getCursor("echo"),
			inputPageSize:  5,
			wantFeatures:   allTestItems[5:10],
			wantNextCursor: getCursor("juliet"), // Based on last item of second page
			wantErr:        false,
		},
		{
			name:           "SecondPage_DefaultSize_Full_Duplicates",
			initialItems:   append(allTestItems, allTestItems[0:5]...),
			inputCursor:    getCursor("echo"),
			inputPageSize:  5,
			wantFeatures:   allTestItems[5:10],
			wantNextCursor: getCursor("juliet"), // Based on last item of second page
			wantErr:        false,
		},
		{
			name:           "LastPage_Remaining",
			initialItems:   allTestItems,
			inputCursor:    getCursor("juliet"),
			inputPageSize:  5,
			wantFeatures:   allTestItems[10:11], // Only 1 item left
			wantNextCursor: "",                  // No more pages
			wantErr:        false,
		},
		{
			name:           "PageSize_1",
			initialItems:   allTestItems,
			inputCursor:    "",
			inputPageSize:  1,
			wantFeatures:   allTestItems[0:1],
			wantNextCursor: getCursor("alpha"),
			wantErr:        false,
		},
		{
			name:           "PageSize_All",
			initialItems:   allTestItems,
			inputCursor:    "",
			inputPageSize:  len(allTestItems), // Page size equals total
			wantFeatures:   allTestItems,
			wantNextCursor: "", // No more pages
			wantErr:        false,
		},
		{
			name:           "PageSize_LargerThanAll",
			initialItems:   allTestItems,
			inputCursor:    "",
			inputPageSize:  len(allTestItems) + 5, // Page size larger than total
			wantFeatures:   allTestItems,
			wantNextCursor: "",
			wantErr:        false,
		},
		{
			name:           "EmptySet",
			initialItems:   nil,
			inputCursor:    "",
			inputPageSize:  5,
			wantFeatures:   nil,
			wantNextCursor: "",
			wantErr:        false,
		},
		{
			name:           "InvalidCursor",
			initialItems:   allTestItems,
			inputCursor:    "not-a-valid-gob-base64-cursor",
			inputPageSize:  5,
			wantFeatures:   nil, // Should be nil for error cases
			wantNextCursor: "",
			wantErr:        true,
		},
		{
			name:           "AboveNonExistentID",
			initialItems:   allTestItems,
			inputCursor:    getCursor("dne"), // A UID that doesn't exist
			inputPageSize:  5,
			wantFeatures:   allTestItems[4:9], // Should return elements above UID.
			wantNextCursor: getCursor("india"),
			wantErr:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFeatureSet(func(t *testItem) string { return t.Name })
			fs.add(tc.initialItems...)
			params := &testListParams{Cursor: tc.inputCursor}
			gotResult, err := paginateList(fs, tc.inputPageSize, params, &testListResult{}, func(res *testListResult, items []*testItem) {
				res.Items = items
			})
			if (err != nil) != tc.wantErr {
				t.Errorf("paginateList(%s) error, got %v, wantErr %v", tc.name, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(tc.wantFeatures, gotResult.Items); diff != "" {
				t.Errorf("paginateList(%s) mismatch (-want +got):\n%s", tc.name, diff)
			}
			if tc.wantNextCursor != gotResult.NextCursor {
				t.Errorf("paginateList(%s) nextCursor, got %v, want %v", tc.name, gotResult.NextCursor, tc.wantNextCursor)
			}
		})
	}
}

func TestServerPaginateVariousPageSizes(t *testing.T) {
	fs := newFeatureSet(func(t *testItem) string { return t.Name })
	fs.add(allTestItems...)
	// Try all possible page sizes, ensuring we get the correct list of items.
	for pageSize := 1; pageSize < len(allTestItems)+1; pageSize++ {
		var gotItems []*testItem
		var nextCursor string
		wantChunks := slices.Collect(slices.Chunk(allTestItems, pageSize))
		index := 0
		// Iterate through all pages, comparing sub-slices to the paginated list.
		for {
			params := &testListParams{Cursor: nextCursor}
			gotResult, err := paginateList(fs, pageSize, params, &testListResult{}, func(res *testListResult, items []*testItem) {
				res.Items = items
			})
			if err != nil {
				t.Fatalf("paginateList() unexpected error for pageSize %d, cursor %q: %v", pageSize, nextCursor, err)
			}
			if diff := cmp.Diff(wantChunks[index], gotResult.Items); diff != "" {
				t.Errorf("paginateList mismatch (-want +got):\n%s", diff)
			}
			gotItems = append(gotItems, gotResult.Items...)
			nextCursor = gotResult.NextCursor
			if nextCursor == "" {
				break
			}
			index++
		}

		if len(gotItems) != len(allTestItems) {
			t.Fatalf("paginateList() returned %d items, want %d", len(allTestItems), len(gotItems))
		}
	}
}

func TestServerCapabilities(t *testing.T) {
	tool := &Tool{Name: "t", InputSchema: &jsonschema.Schema{Type: "object"}}
	testCases := []struct {
		name             string
		configureServer  func(s *Server)
		serverOpts       ServerOptions
		wantCapabilities *ServerCapabilities
	}{
		{
			name:            "No capabilities",
			configureServer: func(s *Server) {},
			wantCapabilities: &ServerCapabilities{
				Logging: &LoggingCapabilities{},
			},
		},
		{
			name: "With prompts",
			configureServer: func(s *Server) {
				s.AddPrompt(&Prompt{Name: "p"}, nil)
			},
			wantCapabilities: &ServerCapabilities{
				Logging: &LoggingCapabilities{},
				Prompts: &PromptCapabilities{ListChanged: true},
			},
		},
		{
			name: "With resources",
			configureServer: func(s *Server) {
				s.AddResource(&Resource{URI: "file:///r"}, nil)
			},
			wantCapabilities: &ServerCapabilities{
				Logging:   &LoggingCapabilities{},
				Resources: &ResourceCapabilities{ListChanged: true},
			},
		},
		{
			name: "With resource templates",
			configureServer: func(s *Server) {
				s.AddResourceTemplate(&ResourceTemplate{URITemplate: "file:///rt"}, nil)
			},
			wantCapabilities: &ServerCapabilities{
				Logging:   &LoggingCapabilities{},
				Resources: &ResourceCapabilities{ListChanged: true},
			},
		},
		{
			name: "With resource subscriptions",
			configureServer: func(s *Server) {
				s.AddResourceTemplate(&ResourceTemplate{URITemplate: "file:///rt"}, nil)
			},
			serverOpts: ServerOptions{
				SubscribeHandler: func(context.Context, *SubscribeRequest) error {
					return nil
				},
				UnsubscribeHandler: func(context.Context, *UnsubscribeRequest) error {
					return nil
				},
			},
			wantCapabilities: &ServerCapabilities{
				Logging:   &LoggingCapabilities{},
				Resources: &ResourceCapabilities{ListChanged: true, Subscribe: true},
			},
		},
		{
			name: "With tools",
			configureServer: func(s *Server) {
				s.AddTool(tool, nil)
			},
			wantCapabilities: &ServerCapabilities{
				Logging: &LoggingCapabilities{},
				Tools:   &ToolCapabilities{ListChanged: true},
			},
		},
		{
			name:            "With completions",
			configureServer: func(s *Server) {},
			serverOpts: ServerOptions{
				CompletionHandler: func(context.Context, *CompleteRequest) (*CompleteResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ServerCapabilities{
				Logging:     &LoggingCapabilities{},
				Completions: &CompletionCapabilities{},
			},
		},
		{
			name: "With all capabilities",
			configureServer: func(s *Server) {
				s.AddPrompt(&Prompt{Name: "p"}, nil)
				s.AddResource(&Resource{URI: "file:///r"}, nil)
				s.AddResourceTemplate(&ResourceTemplate{URITemplate: "file:///rt"}, nil)
				s.AddTool(tool, nil)
			},
			serverOpts: ServerOptions{
				SubscribeHandler: func(context.Context, *SubscribeRequest) error {
					return nil
				},
				UnsubscribeHandler: func(context.Context, *UnsubscribeRequest) error {
					return nil
				},
				CompletionHandler: func(context.Context, *CompleteRequest) (*CompleteResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ServerCapabilities{
				Completions: &CompletionCapabilities{},
				Logging:     &LoggingCapabilities{},
				Prompts:     &PromptCapabilities{ListChanged: true},
				Resources:   &ResourceCapabilities{ListChanged: true, Subscribe: true},
				Tools:       &ToolCapabilities{ListChanged: true},
			},
		},
		{
			name:            "With initial capabilities",
			configureServer: func(s *Server) {},
			serverOpts: ServerOptions{
				HasPrompts:   true,
				HasResources: true,
				HasTools:     true,
			},
			wantCapabilities: &ServerCapabilities{
				Logging:   &LoggingCapabilities{},
				Prompts:   &PromptCapabilities{ListChanged: true},
				Resources: &ResourceCapabilities{ListChanged: true},
				Tools:     &ToolCapabilities{ListChanged: true},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer(testImpl, &tc.serverOpts)
			tc.configureServer(server)
			gotCapabilities := server.capabilities()
			if diff := cmp.Diff(tc.wantCapabilities, gotCapabilities); diff != "" {
				t.Errorf("capabilities() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServerAddResourceTemplate(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		expectPanic bool
	}{
		{"ValidFileTemplate", "file:///{a}/{b}", false},
		{"ValidCustomScheme", "myproto:///{a}", false},
		{"EmptyVariable", "file:///{}/{b}", true},
		{"UnclosedVariable", "file:///{a", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := ResourceTemplate{URITemplate: tt.template}

			defer func() {
				if r := recover(); r != nil {
					if !tt.expectPanic {
						t.Errorf("%s: unexpected panic: %v", tt.name, r)
					}
				} else {
					if tt.expectPanic {
						t.Errorf("%s: expected panic but did not panic", tt.name)
					}
				}
			}()

			s := NewServer(testImpl, nil)
			s.AddResourceTemplate(&rt, nil)
		})
	}
}

// TestServerSessionkeepaliveCancelOverwritten is to verify that `ServerSession.keepaliveCancel` is assigned exactly once,
// ensuring that only a single goroutine is responsible for the session's keepalive ping mechanism.
func TestServerSessionkeepaliveCancelOverwritten(t *testing.T) {
	// Set KeepAlive to a long duration to ensure the keepalive
	// goroutine stays alive for the duration of the test without actually sending
	// ping requests, since we don't have a real client connection established.
	server := NewServer(testImpl, &ServerOptions{KeepAlive: 5 * time.Second})
	ss := &ServerSession{server: server}

	// 1. Initialize the session.
	_, err := ss.initialize(context.Background(), &InitializeParams{})
	if err != nil {
		t.Fatalf("ServerSession initialize failed: %v", err)
	}

	// 2. Call 'initialized' for the first time. This should start the keepalive mechanism.
	_, err = ss.initialized(context.Background(), &InitializedParams{})
	if err != nil {
		t.Fatalf("First initialized call failed: %v", err)
	}
	if ss.keepaliveCancel == nil {
		t.Fatalf("expected ServerSession.keepaliveCancel to be set after the first call of initialized")
	}

	// Save the cancel function and use defer to ensure resources are cleaned up.
	firstCancel := ss.keepaliveCancel
	defer firstCancel()

	// 3. Manually set the field to nil.
	// Do this to facilitate the test's core assertion. The goal is to verify that
	// 'ss.keepaliveCancel' is not assigned a second time. By setting it to nil,
	// we can easily check after the next call if a new keepalive goroutine was started.
	ss.keepaliveCancel = nil

	// 4. Call 'initialized' for the second time. This should return an error.
	_, err = ss.initialized(context.Background(), &InitializedParams{})
	if err == nil {
		t.Fatalf("Expected 'duplicate initialized received' error on second call, got nil")
	}

	// 5. Re-check the field to ensure it remains nil.
	// Since 'initialized' correctly returned an error and did not call
	// 'startKeepalive', the field should remain unchanged.
	if ss.keepaliveCancel != nil {
		t.Fatal("expected ServerSession.keepaliveCancel to be nil after we manually niled it and re-initialized")
	}
}

// panicks reports whether f() panics.
func panics(f func()) (b bool) {
	defer func() {
		b = recover() != nil
	}()
	f()
	return false
}

func TestAddTool(t *testing.T) {
	// AddTool should panic if In or Out are not JSON objects.
	s := NewServer(testImpl, nil)
	if !panics(func() {
		AddTool(s, &Tool{Name: "T1"}, func(context.Context, *CallToolRequest, string) (*CallToolResult, any, error) { return nil, nil, nil })
	}) {
		t.Error("bad In: expected panic")
	}
	if panics(func() {
		AddTool(s, &Tool{Name: "T2"}, func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, any, error) {
			return nil, nil, nil
		})
	}) {
		t.Error("good In: expected no panic")
	}
	if !panics(func() {
		AddTool(s, &Tool{Name: "T2"}, func(context.Context, *CallToolRequest, map[string]any) (*CallToolResult, int, error) {
			return nil, 0, nil
		})
	}) {
		t.Error("bad Out: expected panic")
	}
}

type schema = jsonschema.Schema

func testToolForSchema[In, Out any](t *testing.T, tool *Tool, in string, out Out, wantIn, wantOut any, wantErrContaining string) {
	t.Helper()
	th := func(context.Context, *CallToolRequest, In) (*CallToolResult, Out, error) {
		return nil, out, nil
	}
	gott, goth, err := toolForErr(tool, th)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(wantIn, gott.InputSchema); diff != "" {
		t.Errorf("input: mismatch (-want, +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantOut, gott.OutputSchema); diff != "" {
		t.Errorf("output: mismatch (-want, +got):\n%s", diff)
	}
	ctr := &CallToolRequest{
		Params: &CallToolParamsRaw{
			Arguments: json.RawMessage(in),
		},
	}
	result, err := goth(context.Background(), ctr)
	if wantErrContaining != "" {
		if err == nil {
			t.Errorf("got nil error, want error containing %q", wantErrContaining)
		} else {
			if !strings.Contains(err.Error(), wantErrContaining) {
				t.Errorf("got error %q, want containing %q", err, wantErrContaining)
			}
		}
	} else if err != nil {
		t.Errorf("got error %v, want no error", err)
	}

	if gott.OutputSchema != nil && err == nil && !result.IsError {
		// Check that structured content matches exactly.
		unstructured := result.Content[0].(*TextContent).Text
		structured := string(result.StructuredContent.(json.RawMessage))
		if diff := cmp.Diff(unstructured, structured); diff != "" {
			t.Errorf("Unstructured content does not match structured content exactly (-unstructured +structured):\n%s", diff)
		}
	}
}

// TODO: move this to tool_test.go
func TestToolForSchemas(t *testing.T) {
	// Validate that toolForErr handles schemas properly.
	type in struct {
		P int `json:"p,omitempty"`
	}
	type out struct {
		B bool `json:"b,omitempty"`
	}

	var (
		falseSchema = &schema{Not: &schema{}}
		inSchema    = &schema{Type: "object", AdditionalProperties: falseSchema, Properties: map[string]*schema{"p": {Type: "integer"}}}
		inSchema2   = &schema{Type: "object", AdditionalProperties: falseSchema, Properties: map[string]*schema{"p": {Type: "string"}}}
		outSchema   = &schema{Type: "object", AdditionalProperties: falseSchema, Properties: map[string]*schema{"b": {Type: "boolean"}}}
		outSchema2  = &schema{Type: "object", AdditionalProperties: falseSchema, Properties: map[string]*schema{"b": {Type: "integer"}}}
	)

	// Infer both schemas.
	testToolForSchema[in](t, &Tool{}, `{"p":3}`, out{true}, inSchema, outSchema, "")
	// Validate the input schema: expect an error if it's wrong.
	// We can't test that the output schema is validated, because it's typed.
	testToolForSchema[in](t, &Tool{}, `{"p":"x"}`, out{true}, inSchema, outSchema, `want "integer"`)
	// Ignore type any for output.
	testToolForSchema[in, any](t, &Tool{}, `{"p":3}`, 0, inSchema, nil, "")
	// Input is still validated.
	testToolForSchema[in, any](t, &Tool{}, `{"p":"x"}`, 0, inSchema, nil, `want "integer"`)
	// Tool sets input schema: that is what's used.
	testToolForSchema[in, any](t, &Tool{InputSchema: inSchema2}, `{"p":3}`, 0, inSchema2, nil, `want "string"`)
	// Tool sets output schema: that is what's used, and validation happens.
	testToolForSchema[in, any](t, &Tool{OutputSchema: outSchema2}, `{"p":3}`, out{true},
		inSchema, outSchema2, `want "integer"`)

	// Check a slightly more complicated case.
	type weatherOutput struct {
		Summary string
		AsOf    time.Time
		Source  string
	}
	testToolForSchema[any](t, &Tool{}, `{}`, weatherOutput{},
		&schema{Type: "object"},
		&schema{
			Type:                 "object",
			Required:             []string{"Summary", "AsOf", "Source"},
			AdditionalProperties: falseSchema,
			Properties: map[string]*schema{
				"Summary": {Type: "string"},
				"AsOf":    {Type: "string"},
				"Source":  {Type: "string"},
			},
		},
		"")
}
