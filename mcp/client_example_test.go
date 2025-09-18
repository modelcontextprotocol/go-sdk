// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// !+roots

func Example_roots() {
	ctx := context.Background()

	// Create a client with a single root.
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	c.AddRoots(&mcp.Root{URI: "file://a"})

	// Now create a server with a handler to receive notifications about roots.
	rootsChanged := make(chan struct{})
	handleRootsChanged := func(ctx context.Context, req *mcp.RootsListChangedRequest) {
		rootList, err := req.Session.ListRoots(ctx, nil)
		if err != nil {
			log.Fatal(err)
		}
		var roots []string
		for _, root := range rootList.Roots {
			roots = append(roots, root.URI)
		}
		fmt.Println(roots)
		close(rootsChanged)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, &mcp.ServerOptions{
		RootsListChangedHandler: handleRootsChanged,
	})

	// Connect the server and client...
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		log.Fatal(err)
	}

	clientSession, err := c.Connect(ctx, t2, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer clientSession.Close()

	// ...and add a root. The server is notified about the change.
	c.AddRoots(&mcp.Root{URI: "file://b"})
	<-rootsChanged
	// Output: [file://a file://b]
}

// !-roots

// !+sampling

func Example_sampling() {
	ctx := context.Background()

	// Create a client with a sampling handler.
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, &mcp.ClientOptions{
		CreateMessageHandler: func(_ context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			return &mcp.CreateMessageResult{
				Content: &mcp.TextContent{
					Text: "would have created a message",
				},
			}, nil
		},
	})

	// Connect the server and client...
	ct, st := mcp.NewInMemoryTransports()
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	session, err := s.Connect(ctx, st, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	if _, err := c.Connect(ctx, ct, nil); err != nil {
		log.Fatal(err)
	}

	msg, err := session.CreateMessage(ctx, &mcp.CreateMessageParams{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(msg.Content.(*mcp.TextContent).Text)
	// Output: would have created a message
}

// !-sampling

// !+elicitation

func Example_elicitation() {
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()

	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ss.Close()

	c := mcp.NewClient(testImpl, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"test": "value"}}, nil
		},
	})
	if _, err := c.Connect(ctx, ct, nil); err != nil {
		log.Fatal(err)
	}
	res, err := ss.Elicit(ctx, &mcp.ElicitParams{
		Message: "This should fail",
		RequestedSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"test": {Type: "string"},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content["test"])
	// Output: value
}

// !-elicitation
