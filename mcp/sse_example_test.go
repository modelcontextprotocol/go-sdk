// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AddParams struct {
	X, Y int
}

func (p *AddParams) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[AddParams]()
}

func (p *AddParams) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, p)
}

type AddResult struct {
	Sum int
}

func (r *AddResult) Result() (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("%d", r.Sum)},
		},
	}, nil
}

func Add(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[json.RawMessage]) (*AddResult, error) {
	var args AddParams
	if params.Arguments != nil {
		if err := args.SetParams(params.Arguments); err != nil {
			return nil, err
		}
	}

	return &AddResult{
		Sum: args.X + args.Y,
	}, nil
}

func ExampleSSEHandler() {
	server := mcp.NewServer("adder", "v0.0.1", nil)
	server.AddTools(mcp.NewServerTool[*AddParams, *AddResult]("add", "add two numbers", Add))

	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server })
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	transport := mcp.NewSSEClientTransport(httpServer.URL, nil)
	client := mcp.NewClient("test", "v1.0.0", nil)
	cs, err := client.Connect(ctx, transport)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add",
		Arguments: map[string]any{"x": 1, "y": 2},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content[0].(*mcp.TextContent).Text)

	// Output: 3
}
