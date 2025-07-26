// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String("http", "", "use SSE HTTP at this address")

type SayHiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[SayHiParams]) (*mcp.CallToolResultFor[any], error) {
	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Hi " + params.Arguments.Name},
		},
	}, nil
}

func main() {
	flag.Parse()

	if httpAddr == nil || *httpAddr == "" {
		log.Fatal("http address not set")
	}

	server1 := mcp.NewServer(&mcp.Implementation{Name: "greeter1"}, nil)
	mcp.AddTool(server1, &mcp.Tool{Name: "greet1", Description: "say hi"}, SayHi)

	server2 := mcp.NewServer(&mcp.Implementation{Name: "greeter2"}, nil)
	mcp.AddTool(server2, &mcp.Tool{Name: "greet2", Description: "say hello"}, SayHi)

	log.Printf("MCP servers serving at %s", *httpAddr)
	handler := mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
		url := request.URL.Path
		log.Printf("Handling request for URL %s\n", url)
		switch url {
		case "/greeter1":
			return server1
		case "/greeter2":
			return server2
		default:
			return nil
		}
	})
	log.Fatal(http.ListenAndServe(*httpAddr, handler))
}
