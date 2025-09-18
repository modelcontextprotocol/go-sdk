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

// !+prompts

func Example_prompts() {
	ctx := context.Background()

	promptHandler := func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "Hi prompt",
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: "Say hi to " + req.Params.Arguments["name"]},
				},
			},
		}, nil
	}

	// Create a server with a single prompt.
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	prompt := &mcp.Prompt{
		Name: "greet",
		Arguments: []*mcp.PromptArgument{
			{
				Name:        "name",
				Description: "the name of the person to greet",
				Required:    true,
			},
		},
	}
	s.AddPrompt(prompt, promptHandler)

	// Create a client.
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)

	// Connect the server and client.
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		log.Fatal(err)
	}
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		log.Fatal(err)
	}

	// List the prompts.
	for p, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(p.Name)
	}

	// Get the prompt.
	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      "greet",
		Arguments: map[string]string{"name": "Pat"},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, msg := range res.Messages {
		fmt.Println(msg.Role, msg.Content.(*mcp.TextContent).Text)
	}
	// Output:
	// greet
	// user Say hi to Pat
}

// !-prompts
