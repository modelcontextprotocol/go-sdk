// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AddParams struct {
	X, Y int
}

func Add(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[AddParams]) (*mcp.CallToolResultFor[any], error) {
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("%d", params.Arguments.X+params.Arguments.Y)},
		},
	}, nil
}

func ExampleSSEHandler() {
	server := mcp.NewServer(&mcp.Implementation{Name: "adder", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "add", Description: "add two numbers"}, Add)

	handler := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return server })
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	transport := mcp.NewSSEClientTransport(httpServer.URL, nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1.0.0"}, nil)
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
