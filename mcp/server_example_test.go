// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

func ExampleServer() {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	server := mcp.NewServer(&mcp.Implementation{Name: "greeter", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "say hi"}, SayHi)

	serverSession, err := server.Connect(ctx, serverTransport)
	if err != nil {
		log.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "client"}, nil)
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
	server := mcp.NewServer(testImpl, nil)
	client := mcp.NewClient(testImpl, nil)
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
