// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

// TODO(v0.3.0): rewrite this test.
// func TestToolValidate(t *testing.T) {
// 	// Check that the tool returned from NewServerTool properly validates its input schema.

// 	type req struct {
// 		I int
// 		B bool
// 		S string `json:",omitempty"`
// 		P *int   `json:",omitempty"`
// 	}

// 	dummyHandler := func(context.Context, *CallToolRequest, req) (*CallToolResultFor[any], error) {
// 		return nil, nil
// 	}

// 	st, err := newServerTool(&Tool{Name: "test", Description: "test"}, dummyHandler)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	for _, tt := range []struct {
// 		desc string
// 		args map[string]any
// 		want string // error should contain this string; empty for success
// 	}{
// 		{
// 			"both required",
// 			map[string]any{"I": 1, "B": true},
// 			"",
// 		},
// 		{
// 			"optional",
// 			map[string]any{"I": 1, "B": true, "S": "foo"},
// 			"",
// 		},
// 		{
// 			"wrong type",
// 			map[string]any{"I": 1.5, "B": true},
// 			"cannot unmarshal",
// 		},
// 		{
// 			"extra property",
// 			map[string]any{"I": 1, "B": true, "C": 2},
// 			"unknown field",
// 		},
// 		{
// 			"value for pointer",
// 			map[string]any{"I": 1, "B": true, "P": 3},
// 			"",
// 		},
// 		{
// 			"null for pointer",
// 			map[string]any{"I": 1, "B": true, "P": nil},
// 			"",
// 		},
// 	} {
// 		t.Run(tt.desc, func(t *testing.T) {
// 			raw, err := json.Marshal(tt.args)
// 			if err != nil {
// 				t.Fatal(err)
// 			}
// 			_, err = st.handler(context.Background(), &ServerRequest[*CallToolParamsFor[json.RawMessage]]{
// 				Params: &CallToolParamsFor[json.RawMessage]{Arguments: json.RawMessage(raw)},
// 			})
// 			if err == nil && tt.want != "" {
// 				t.Error("got success, wanted failure")
// 			}
// 			if err != nil {
// 				if tt.want == "" {
// 					t.Fatalf("failed with:\n%s\nwanted success", err)
// 				}
// 				if !strings.Contains(err.Error(), tt.want) {
// 					t.Fatalf("got:\n%s\nwanted to contain %q", err, tt.want)
// 				}
// 			}
// 		})
// 	}
// }

// TestNilParamsHandling tests that nil parameters don't cause panic in unmarshalParams.
// This addresses a vulnerability where missing or null parameters could crash the server.
// func TestNilParamsHandling(t *testing.T) {
// 	// Define test types for clarity
// 	type TestArgs struct {
// 		Name  string `json:"name"`
// 		Value int    `json:"value"`
// 	}

// 	// Simple test handler
// 	testHandler := func(ctx context.Context, req *ServerRequest[**GetPromptParams]) (*GetPromptResult, error) {
// 		result := "processed: " + req.Params.Arguments.Name
// 		return &CallToolResultFor[string]{StructuredContent: result}, nil
// 	}

// 	methodInfo := newServerMethodInfo(testHandler, missingParamsOK)

// 	// Helper function to test that unmarshalParams doesn't panic and handles nil gracefully
// 	mustNotPanic := func(t *testing.T, rawMsg json.RawMessage, expectNil bool) Params {
// 		t.Helper()

// 		defer func() {
// 			if r := recover(); r != nil {
// 				t.Fatalf("unmarshalParams panicked: %v", r)
// 			}
// 		}()

// 		params, err := methodInfo.unmarshalParams(rawMsg)
// 		if err != nil {
// 			t.Fatalf("unmarshalParams failed: %v", err)
// 		}

// 		if expectNil {
// 			if params != nil {
// 				t.Fatalf("Expected nil params, got %v", params)
// 			}
// 			return params
// 		}

// 		if params == nil {
// 			t.Fatal("unmarshalParams returned unexpected nil")
// 		}

// 		// Verify the result can be used safely
// 		typedParams := params.(TestParams)
// 		_ = typedParams.Name
// 		_ = typedParams.Arguments.Name
// 		_ = typedParams.Arguments.Value

// 		return params
// 	}

// 	// Test different nil parameter scenarios - with missingParamsOK flag, nil/null should return nil
// 	t.Run("missing_params", func(t *testing.T) {
// 		mustNotPanic(t, nil, true) // Expect nil with missingParamsOK flag
// 	})

// 	t.Run("explicit_null", func(t *testing.T) {
// 		mustNotPanic(t, json.RawMessage(`null`), true) // Expect nil with missingParamsOK flag
// 	})

// 	t.Run("empty_object", func(t *testing.T) {
// 		mustNotPanic(t, json.RawMessage(`{}`), false) // Empty object should create valid params
// 	})

// 	t.Run("valid_params", func(t *testing.T) {
// 		rawMsg := json.RawMessage(`{"name":"test","arguments":{"name":"hello","value":42}}`)
// 		params := mustNotPanic(t, rawMsg, false)

// 		// For valid params, also verify the values are parsed correctly
// 		typedParams := params.(TestParams)
// 		if typedParams.Name != "test" {
// 			t.Errorf("Expected name 'test', got %q", typedParams.Name)
// 		}
// 		if typedParams.Arguments.Name != "hello" {
// 			t.Errorf("Expected argument name 'hello', got %q", typedParams.Arguments.Name)
// 		}
// 		if typedParams.Arguments.Value != 42 {
// 			t.Errorf("Expected argument value 42, got %d", typedParams.Arguments.Value)
// 		}
// 	})
// }

// TestNilParamsEdgeCases tests edge cases to ensure we don't over-fix
// func TestNilParamsEdgeCases(t *testing.T) {
// 	type TestArgs struct {
// 		Name  string `json:"name"`
// 		Value int    `json:"value"`
// 	}
// 	type TestParams = *CallToolParamsFor[TestArgs]

// 	testHandler := func(context.Context, *ServerRequest[TestParams]) (*CallToolResultFor[string], error) {
// 		return &CallToolResultFor[string]{StructuredContent: "test"}, nil
// 	}

// 	methodInfo := newServerMethodInfo(testHandler, missingParamsOK)

// 	// These should fail normally, not be treated as nil params
// 	invalidCases := []json.RawMessage{
// 		json.RawMessage(""),       // empty string - should error
// 		json.RawMessage("[]"),     // array - should error
// 		json.RawMessage(`"null"`), // string "null" - should error
// 		json.RawMessage("0"),      // number - should error
// 		json.RawMessage("false"),  // boolean - should error
// 	}

// 	for i, rawMsg := range invalidCases {
// 		t.Run(fmt.Sprintf("invalid_case_%d", i), func(t *testing.T) {
// 			params, err := methodInfo.unmarshalParams(rawMsg)
// 			if err == nil && params == nil {
// 				t.Error("Should not return nil params without error")
// 			}
// 		})
// 	}

// 	// Test that methods without missingParamsOK flag properly reject nil params
// 	t.Run("reject_when_params_required", func(t *testing.T) {
// 		methodInfoStrict := newServerMethodInfo(testHandler, 0) // No missingParamsOK flag

// 		testCases := []struct {
// 			name   string
// 			params json.RawMessage
// 		}{
// 			{"nil_params", nil},
// 			{"null_params", json.RawMessage(`null`)},
// 		}

// 		for _, tc := range testCases {
// 			t.Run(tc.name, func(t *testing.T) {
// 				_, err := methodInfoStrict.unmarshalParams(tc.params)
// 				if err == nil {
// 					t.Error("Expected error for required params, got nil")
// 				}
// 				if !strings.Contains(err.Error(), "missing required \"params\"") {
// 					t.Errorf("Expected 'missing required params' error, got: %v", err)
// 				}
// 			})
// 		}
// 	})
// }
