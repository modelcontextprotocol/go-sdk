// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String("http", "", "use SSE HTTP at this address")

type SayHiParams struct {
	Name string `json:"name"`
}

func (s *SayHiParams) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[SayHiParams]()
}

func (s *SayHiParams) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, s)
}

type SayHiResult struct{}

func (s *SayHiResult) Result() (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[json.RawMessage]) (*SayHiResult, error) {
	var args SayHiParams
	if params.Arguments != nil {
		if err := args.SetParams(params.Arguments); err != nil {
			return nil, err
		}
	}

	result := &SayHiResult{}
	toolResult, err := result.Result()
	if err != nil {
		return nil, err
	}

	toolResult.Content = []mcp.Content{
		&mcp.TextContent{Text: "Hi " + args.Name},
	}

	return result, nil
}

func main() {
	flag.Parse()

	if httpAddr == nil || *httpAddr == "" {
		log.Fatal("http address not set")
	}

	server1 := mcp.NewServer("greeter1", "v0.0.1", nil)
	server1.AddTools(mcp.NewServerTool[*SayHiParams, *SayHiResult]("greet1", "say hi", SayHi))

	server2 := mcp.NewServer("greeter2", "v0.0.1", nil)
	server2.AddTools(mcp.NewServerTool[*SayHiParams, *SayHiResult]("greet2", "say hello", SayHi))

	log.Printf("MCP servers serving at %s\n", *httpAddr)
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
	http.ListenAndServe(*httpAddr, handler)
}
