// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// !+
package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type HiParams struct {
	Name string `json:"name"`
}

func (p *HiParams) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[HiParams]()
}

func (p *HiParams) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, p)
}

type HiResult struct{}

func (r *HiResult) Result() (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[json.RawMessage]) (*HiResult, error) {
	var args HiParams
	if params.Arguments != nil {
		if err := args.SetParams(params.Arguments); err != nil {
			return nil, err
		}
	}

	result := &HiResult{}
	toolResult, err := result.Result()
	if err != nil {
		return nil, err
	}

	toolResult.Content = []mcp.Content{&mcp.TextContent{Text: "Hi " + args.Name}}

	return result, nil
}

func main() {
	// Create a server with a single tool.
	server := mcp.NewServer("greeter", "v1.0.0", nil)
	server.AddTools(
		mcp.NewServerTool[*HiParams, *HiResult]("greet", "say hi", SayHi),
	)
	// Run the server over stdin/stdout, until the client disconnects
	if err := server.Run(context.Background(), mcp.NewStdioTransport()); err != nil {
		log.Fatal(err)
	}
}

// !-
