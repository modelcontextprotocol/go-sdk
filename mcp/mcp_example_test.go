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

// !+lifecycle

func Example_lifeCycle() {
	ctx := context.Background()

	// Create a client and server.
	// Wait for the client to initialize the session.
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, &mcp.ServerOptions{
		InitializedHandler: func(context.Context, *mcp.InitializedRequest) {
			fmt.Println("initialized!")
		},
	})

	// Connect the server and client using in-memory transports.
	//
	// Connect the server first so that it's ready to receive initialization
	// messages from the client.
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		log.Fatal(err)
	}
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Now shut down the session by closing the client, and waiting for the
	// server session to end.
	if err := clientSession.Close(); err != nil {
		log.Fatal(err)
	}
	if err := serverSession.Wait(); err != nil {
		log.Fatal(err)
	}
	// Output: initialized!
}

// !-lifecycle

// !+progress

func Example_progress() {
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "makeProgress"}, func(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		token, ok := req.Params.GetMeta()["progressToken"]
		if ok {
			for i := range 3 {
				params := &mcp.ProgressNotificationParams{
					Message:       fmt.Sprintf("progress %d", i),
					ProgressToken: token,
					Progress:      float64(i),
				}
				req.Session.NotifyProgress(ctx, params) // ignore error
			}
		}
		return &mcp.CallToolResult{}, nil, nil
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			fmt.Println(req.Params.Message)
		},
	})
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		log.Fatal(err)
	}

	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "makeProgress",
		Meta: mcp.Meta{"progressToken": "abc123"},
	}); err != nil {
		log.Fatal(err)
	}
	// Output:
	// progress 0
	// progress 1
	// progress 2
}

// !-progress
