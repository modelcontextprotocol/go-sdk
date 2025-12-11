// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// The conformance server implements features required for MCP conformance testing.
// It mirrors the functionality of the TypeScript conformance server at
// https://github.com/modelcontextprotocol/conformance/blob/main/examples/servers/typescript/everything-server.ts
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yosida95/uritemplate/v3"
)

var (
	httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")
)

const watchedResourceURI = "test://watched-resource"

func main() {
	flag.Parse()

	opts := &mcp.ServerOptions{
		CompletionHandler:  completionHandler,
		SubscribeHandler:   subscribeHandler,
		UnsubscribeHandler: unsubscribeHandler,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "mcp-conformance-test-server",
		Version: "1.0.0",
	}, opts)

	// Register server features.
	registerTools(server)
	registerResources(server)
	registerPrompts(server)

	// Start the watched resource auto-update goroutine.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchedResourceUpdater(ctx, server)

	// Serve over stdio, or streamable HTTP if -http is set.
	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("Conformance server listening at %s", *httpAddr)
		log.Fatal(http.ListenAndServe(*httpAddr, handler))
	} else {
		t := &mcp.StdioTransport{}
		if err := server.Run(ctx, t); err != nil {
			log.Printf("Server failed: %v", err)
			os.Exit(1)
		}
	}
}

// watchedResourceUpdater sends resource update notifications every 3 seconds.
func watchedResourceUpdater(ctx context.Context, server *mcp.Server) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			server.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{
				URI: watchedResourceURI,
			})
		}
	}
}

// =============================================================================
// Tools
// =============================================================================

func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_simple_text",
		Description: "Tests simple text content response",
	}, testSimpleTextHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_image_content",
		Description: "Tests image content response",
	}, testImageContentHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_audio_content",
		Description: "Tests audio content response",
	}, testAudioContentHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_embedded_resource",
		Description: "Tests embedded resource content response",
	}, testEmbeddedResourceHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_multiple_content_types",
		Description: "Tests response with multiple content types (text, image, resource)",
	}, testMultipleContentTypesHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_tool_with_logging",
		Description: "Tests tool that emits log messages during execution",
	}, testToolWithLoggingHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_tool_with_progress",
		Description: "Tests tool that reports progress notifications",
	}, testToolWithProgressHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_error_handling",
		Description: "Tests error response handling",
	}, testErrorHandlingHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_sampling",
		Description: "Tests server-initiated sampling (LLM completion request)",
	}, testSamplingHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_elicitation",
		Description: "Tests server-initiated elicitation (user input request)",
	}, testElicitationHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_elicitation_sep1034_defaults",
		Description: "Tests elicitation with default values per SEP-1034",
	}, testElicitationDefaultsHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_elicitation_sep1330_enums",
		Description: "Tests elicitation with enum schema improvements per SEP-1330",
	}, testElicitationEnumsHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "json_schema_2020_12_tool",
		Description: "Tool with JSON Schema 2020-12 features for conformance testing (SEP-1613)",
		InputSchema: json.RawMessage(`{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type": "object",
			"$defs": {
				"address": {
					"type": "object",
					"properties": {
						"street": { "type": "string" },
						"city": { "type": "string" }
					}
				}
			},
			"properties": {
				"name": { "type": "string" },
				"address": { "$ref": "#/$defs/address" }
			},
			"additionalProperties": false
		}`),
	}, jsonSchema202012Handler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "test_reconnection",
		Description: "Tests SSE stream disconnection and client reconnection (SEP-1699). Server will close the stream mid-call and send the result after client reconnects.",
	}, testReconnectionHandler)
}

// Tool handlers

func testSimpleTextHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "This is a simple text response for testing."},
		},
	}, nil, nil
}

func testImageContentHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{
				Data:     imageData(),
				MIMEType: "image/png",
			},
		},
	}, nil, nil
}

func testAudioContentHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.AudioContent{
				Data:     audioData(),
				MIMEType: "audio/wav",
			},
		},
	}, nil, nil
}

func testEmbeddedResourceHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.EmbeddedResource{
				Resource: &mcp.ResourceContents{
					URI:      "test://embedded-resource",
					MIMEType: "text/plain",
					Text:     "This is an embedded resource",
				},
			},
		},
	}, nil, nil
}

func testMultipleContentTypesHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "This is text content"},
			&mcp.ImageContent{
				Data:     imageData(),
				MIMEType: "image/png",
			},
			&mcp.EmbeddedResource{
				Resource: &mcp.ResourceContents{
					URI:      "test://embedded-in-multiple",
					MIMEType: "text/plain",
					Text:     "This is an embedded resource",
				},
			},
		},
	}, nil, nil
}

func testToolWithLoggingHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	// Emit three info-level log messages
	req.Session.Log(ctx, &mcp.LoggingMessageParams{
		Level: "info",
		Data:  "Tool execution started",
	})
	time.Sleep(50 * time.Millisecond)
	req.Session.Log(ctx, &mcp.LoggingMessageParams{
		Level: "info",
		Data:  "Tool processing data",
	})
	time.Sleep(50 * time.Millisecond)
	req.Session.Log(ctx, &mcp.LoggingMessageParams{
		Level: "info",
		Data:  "Tool execution completed",
	})

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Tool with logging executed successfully"},
		},
	}, nil, nil
}

func testToolWithProgressHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	// Get progress token from the request if provided
	progressToken := req.Params.GetProgressToken()

	// Send three progress notifications (0%, 50%, 100%)
	total := 100.0
	steps := []float64{0, 50, 100}
	for _, progress := range steps {
		req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: progressToken,
			Progress:      progress,
			Total:         total,
			Message:       fmt.Sprintf("Completed step %.0f of %.0f", progress, total),
		})
		time.Sleep(50 * time.Millisecond)
	}

	// Return the progress token value as the response (matching TypeScript behavior)
	tokenStr := fmt.Sprintf("%v", progressToken)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: tokenStr},
		},
	}, nil, nil
}

func testErrorHandlingHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	return nil, nil, errors.New("this tool intentionally returns an error for testing")
}

type samplingInput struct {
	Prompt string `json:"prompt" jsonschema:"The prompt to send to the LLM"`
}

func testSamplingHandler(ctx context.Context, req *mcp.CallToolRequest, input samplingInput) (*mcp.CallToolResult, any, error) {
	// Request LLM completion from the client
	result, err := req.Session.CreateMessage(ctx, &mcp.CreateMessageParams{
		Messages: []*mcp.SamplingMessage{
			{
				Role: "user",
				Content: &mcp.TextContent{
					Text: input.Prompt,
				},
			},
		},
		MaxTokens: 100,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("sampling failed: %w", err)
	}

	// Extract the text response from the result
	var responseText string
	if tc, ok := result.Content.(*mcp.TextContent); ok {
		responseText = tc.Text
	} else {
		responseText = "(non-text response)"
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("LLM response: %s", responseText)},
		},
	}, nil, nil
}

type elicitationInput struct {
	Message string `json:"message" jsonschema:"The message to show the user"`
}

func testElicitationHandler(ctx context.Context, req *mcp.CallToolRequest, input elicitationInput) (*mcp.CallToolResult, any, error) {
	result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: input.Message,
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"username": {
					Type:        "string",
					Description: "Your preferred username",
				},
			},
			Required: []string{"username"},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("elicitation failed: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Elicitation result: action=%s, content=%v", result.Action, result.Content)},
		},
	}, nil, nil
}

func testElicitationDefaultsHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: "Test defaults for primitives",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "User name",
					"default":     "John Doe",
				},
				"age": map[string]any{
					"type":        "integer",
					"description": "User age",
					"default":     30,
				},
				"score": map[string]any{
					"type":        "number",
					"description": "User score",
					"default":     95.5,
				},
				"status": map[string]any{
					"type":        "string",
					"description": "User status",
					"enum":        []string{"active", "inactive", "pending"},
					"default":     "active",
				},
				"verified": map[string]any{
					"type":        "boolean",
					"description": "Verification status",
					"default":     true,
				},
			},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("elicitation failed: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Elicitation result: action=%s, content=%v", result.Action, result.Content)},
		},
	}, nil, nil
}

func testElicitationEnumsHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	result, err := req.Session.Elicit(ctx, &mcp.ElicitParams{
		Message: "Test enum schemas",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				// Basic enum without titles
				"untitledSingle": map[string]any{
					"type": "string",
					"enum": []string{"option1", "option2", "option3"},
				},
				// Enum with titles using oneOf
				"titledSingle": map[string]any{
					"type": "string",
					"oneOf": []map[string]any{
						{"const": "value1", "title": "First Option"},
						{"const": "value2", "title": "Second Option"},
						{"const": "value3", "title": "Third Option"},
					},
				},
				// Legacy enum with enumNames
				"legacyEnum": map[string]any{
					"type":      "string",
					"enum":      []string{"opt1", "opt2", "opt3"},
					"enumNames": []string{"Option One", "Option Two", "Option Three"},
				},
				// Multi-select without titles
				"untitledMulti": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 3,
					"items": map[string]any{
						"type": "string",
						"enum": []string{"option1", "option2", "option3"},
					},
				},
				// Multi-select with titles using anyOf
				"titledMulti": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 3,
					"items": map[string]any{
						"type": "string",
						"anyOf": []map[string]any{
							{"const": "value1", "title": "First Option"},
							{"const": "value2", "title": "Second Option"},
							{"const": "value3", "title": "Third Option"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("elicitation failed: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Elicitation result: action=%s, content=%v", result.Action, result.Content)},
		},
	}, nil, nil
}

type jsonSchemaAddress struct {
	Street string `json:"street"`
	City   string `json:"city"`
}

type jsonSchemaInput struct {
	Name    string             `json:"name"`
	Address *jsonSchemaAddress `json:"address"`
}

func jsonSchema202012Handler(ctx context.Context, req *mcp.CallToolRequest, input jsonSchemaInput) (*mcp.CallToolResult, any, error) {
	// Echo back the arguments received
	var addressStr string
	if input.Address != nil {
		addressStr = fmt.Sprintf("{street: %q, city: %q}", input.Address.Street, input.Address.City)
	} else {
		addressStr = "nil"
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Received: name=%q, address=%s", input.Name, addressStr),
			},
		},
	}, nil, nil
}

func testReconnectionHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	// Close the SSE stream to trigger client reconnection (SEP-1699)
	if req.Extra != nil && req.Extra.CloseSSEStream != nil {
		req.Extra.CloseSSEStream(mcp.CloseSSEStreamArgs{RetryAfter: 10 * time.Millisecond})
	}

	// Wait for client to reconnect
	time.Sleep(100 * time.Millisecond)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: "Reconnection test completed successfully. If you received this, the client properly reconnected after stream closure.",
			},
		},
	}, nil, nil
}

// =============================================================================
// Resources
// =============================================================================

func registerResources(server *mcp.Server) {
	server.AddResource(&mcp.Resource{
		Name:        "static-text",
		Description: "A static text resource for testing",
		MIMEType:    "text/plain",
		URI:         "test://static-text",
	}, staticTextHandler)

	server.AddResource(&mcp.Resource{
		Name:        "static-binary",
		Description: "A static binary resource (image) for testing",
		MIMEType:    "image/png",
		URI:         "test://static-binary",
	}, staticBinaryHandler)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "template",
		Description: "A resource template with parameter substitution",
		MIMEType:    "application/json",
		URITemplate: "test://template/{id}/data",
	}, templateResourceHandler)

	server.AddResource(&mcp.Resource{
		Name:        "watched-resource",
		Description: "A resource that auto-updates every 3 seconds",
		MIMEType:    "text/plain",
		URI:         watchedResourceURI,
	}, watchedResourceHandler)
}

func staticTextHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     "This is the content of the static text resource.",
			},
		},
	}, nil
}

func staticBinaryHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "image/png",
				Blob:     imageData(),
			},
		},
	}, nil
}

// templatePattern is the compiled URI template for the template resource.
var templatePattern = uritemplate.MustNew("test://template/{id}/data")

func templateResourceHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	// Extract the ID from the URI using the template pattern
	uri := req.Params.URI
	match := templatePattern.Regexp().FindStringSubmatch(uri)
	id := ""
	if len(match) > 1 {
		id = match[1]
	}

	jsonContent := fmt.Sprintf(`{"id": "%s", "templateTest": true, "data": "Data for ID: %s"}`, id, id)
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      uri,
				MIMEType: "application/json",
				Text:     jsonContent,
			},
		},
	}, nil
}

func watchedResourceHandler(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      req.Params.URI,
				MIMEType: "text/plain",
				Text:     "Watched resource content",
			},
		},
	}, nil
}

// =============================================================================
// Prompts
// =============================================================================

func registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "test_simple_prompt",
		Title:       "Simple Test Prompt",
		Description: "A simple prompt without arguments",
	}, simplePromptHandler)

	server.AddPrompt(&mcp.Prompt{
		Name:        "test_prompt_with_arguments",
		Title:       "Prompt With Arguments",
		Description: "A prompt with required arguments",
		Arguments: []*mcp.PromptArgument{
			{Name: "arg1", Description: "First test argument", Required: true},
			{Name: "arg2", Description: "Second test argument", Required: true},
		},
	}, promptWithArgumentsHandler)

	server.AddPrompt(&mcp.Prompt{
		Name:        "test_prompt_with_embedded_resource",
		Title:       "Prompt With Embedded Resource",
		Description: "A prompt that includes an embedded resource",
		Arguments: []*mcp.PromptArgument{
			{Name: "resourceUri", Description: "URI of the resource to embed", Required: true},
		},
	}, promptWithEmbeddedResourceHandler)

	server.AddPrompt(&mcp.Prompt{
		Name:        "test_prompt_with_image",
		Title:       "Prompt With Image",
		Description: "A prompt that includes image content",
	}, promptWithImageHandler)
}

func simplePromptHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "A simple test prompt",
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: "This is a simple prompt for testing."},
			},
		},
	}, nil
}

func promptWithArgumentsHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	arg1 := req.Params.Arguments["arg1"]
	arg2 := req.Params.Arguments["arg2"]

	return &mcp.GetPromptResult{
		Description: "A prompt with arguments",
		Messages: []*mcp.PromptMessage{
			{
				Role: "user",
				Content: &mcp.TextContent{
					Text: fmt.Sprintf("Prompt with arguments: arg1='%s', arg2='%s'", arg1, arg2),
				},
			},
		},
	}, nil
}

func promptWithEmbeddedResourceHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	resourceUri := req.Params.Arguments["resourceUri"]

	return &mcp.GetPromptResult{
		Description: "A prompt with an embedded resource",
		Messages: []*mcp.PromptMessage{
			{
				Role: "user",
				Content: &mcp.EmbeddedResource{
					Resource: &mcp.ResourceContents{
						URI:      resourceUri,
						MIMEType: "text/plain",
						Text:     "Embedded resource content for testing.",
					},
				},
			},
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: "Please process the embedded resource above."},
			},
		},
	}, nil
}

func promptWithImageHandler(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "A prompt with an image",
		Messages: []*mcp.PromptMessage{
			{
				Role: "user",
				Content: &mcp.ImageContent{
					Data:     imageData(),
					MIMEType: "image/png",
				},
			},
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: "Please analyze the image above."},
			},
		},
	}, nil
}

// =============================================================================
// Server handlers
// =============================================================================

func completionHandler(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	// Return empty completion - just acknowledging the capability
	return &mcp.CompleteResult{
		Completion: mcp.CompletionResultDetails{
			Values: []string{},
			Total:  0,
		},
	}, nil
}

func subscribeHandler(ctx context.Context, req *mcp.SubscribeRequest) error {
	// The SDK handles subscription tracking internally via Server.ResourceUpdated()
	return nil
}

func unsubscribeHandler(ctx context.Context, req *mcp.UnsubscribeRequest) error {
	// The SDK handles subscription tracking internally
	return nil
}

// =============================================================================
// Helper functions
// =============================================================================

// Base64-encoded minimal test files, copied from the typescript conformance example.
const (
	// Minimal 1x1 red PNG image
	testImageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
	// Minimal WAV audio file (silence)
	testAudioBase64 = "UklGRiYAAABXQVZFZm10IBAAAAABAAEAQB8AAAB9AAACABAAZGF0YQIAAAA="
)

func imageData() []byte {
	data, err := base64.StdEncoding.DecodeString(testImageBase64)
	if err != nil {
		panic("invalid testImageBase64: " + err.Error())
	}
	return data
}

func audioData() []byte {
	data, err := base64.StdEncoding.DecodeString(testAudioBase64)
	if err != nil {
		panic("invalid testAudioBase64: " + err.Error())
	}
	return data
}
