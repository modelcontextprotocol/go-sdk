package main

import (
	"context"
	_ "embed"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed index.html
var viewHTML string

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "example-app-server",
		Version: "v0.1.0",
	}, nil)

	// Register a UI resource that serves the pre-built HTML page,
	// which pre-bundles the JS MCP Apps SDK into the HTML page.
	// SDK: https://github.com/modelcontextprotocol/ext-apps
	server.AddResource(&mcp.Resource{
		URI:      "ui://example/view",
		Name:     "example-view",
		MIMEType: "text/html;profile=mcp-app",
		Meta: mcp.Meta{
			"ui": map[string]any{
				"csp": map[string]any{},
			},
		},
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "ui://example/view",
				MIMEType: "text/html;profile=mcp-app",
				Text:     viewHTML,
				Meta: mcp.Meta{
					"ui": map[string]any{
						"csp": map[string]any{},
					},
				},
			}},
		}, nil
	})

	// Tool: show-data — visible to both the model and the app UI.
	type ShowDataInput struct {
		Data string `json:"data" jsonschema:"data to display"`
	}
	type ShowDataOutput struct {
		Data string `json:"data"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "show-data",
		Description: "Shows data in the UI view",
		Meta: mcp.Meta{
			"ui": map[string]any{
				"resourceUri": "ui://example/view",
				"visibility":  []string{"model", "app"},
			},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ShowDataInput) (*mcp.CallToolResult, ShowDataOutput, error) {
		output := ShowDataOutput{Data: input.Data}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: input.Data},
			},
		}, output, nil
	})

	// Tool: count-words — app-only tool the UI calls via callServerTool.
	type CountInput struct {
		Text string `json:"text" jsonschema:"text to count words in"`
	}
	type CountOutput struct {
		WordCount int `json:"wordCount"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "count-words",
		Description: "Counts words in text",
		Meta: mcp.Meta{
			"ui": map[string]any{
				"visibility": []string{"app"},
			},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CountInput) (*mcp.CallToolResult, CountOutput, error) {
		words := 0
		inWord := false
		for _, r := range input.Text {
			if r == ' ' || r == '\t' || r == '\n' {
				inWord = false
			} else if !inWord {
				inWord = true
				words++
			}
		}
		return nil, CountOutput{WordCount: words}, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
