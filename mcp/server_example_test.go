// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SayHiParams struct {
	Name string `json:"name"`
}

func (s *SayHiParams) Schema() (*jsonschema.Schema, error) {
	return jsonschema.For[SayHiParams]()
}

func (s *SayHiParams) SetParams(raw json.RawMessage) error {
	return json.Unmarshal(raw, s)
}

type SayHiResult struct {
	Message string
}

func (s *SayHiResult) Result() (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: s.Message},
		},
	}, nil
}

func SayHi(ctx context.Context, cc *mcp.ServerSession, params *mcp.CallToolParamsFor[json.RawMessage]) (*SayHiResult, error) {
	var args SayHiParams
	if params.Arguments != nil {
		if err := args.SetParams(params.Arguments); err != nil {
			return nil, err
		}
	}

	return &SayHiResult{
		Message: "Hi " + args.Name,
	}, nil
}

func ExampleServer() {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer("greeter", "v0.0.1", nil)
	server.AddTools(mcp.NewServerTool[*SayHiParams, *SayHiResult](
		"greet", "say hi", SayHi))

	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}

	client := mcp.NewClient("client", "v0.0.1", nil)
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		log.Fatal(err)
	}

	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "user"},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content[0].(*mcp.TextContent).Text)

	clientSession.Close()
	serverSession.Wait()

	// Output: Hi user
}

// createSessions creates and connects an in-memory client and server session for testing purposes.
func createSessions(ctx context.Context) (*mcp.ClientSession, *mcp.ServerSession, *mcp.Server) {
	server := mcp.NewServer("server", "v0.0.1", nil)
	client := mcp.NewClient("client", "v0.0.1", nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}
	clientSession, err := client.Connect(ctx, clientTransport)
	if err != nil {
		log.Fatal(err)
	}
	return clientSession, serverSession, server
}
