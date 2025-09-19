// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"sync/atomic"

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
	defer cs.Close()

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

// !+logging

func Example_logging() {
	ctx := context.Background()

	// Create a server.
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)

	// Create a client that displays log messages.
	done := make(chan struct{}) // solely for the example
	var nmsgs atomic.Int32
	c := mcp.NewClient(
		&mcp.Implementation{Name: "client", Version: "v0.0.1"},
		&mcp.ClientOptions{
			LoggingMessageHandler: func(_ context.Context, r *mcp.LoggingMessageRequest) {
				m := r.Params.Data.(map[string]any)
				fmt.Println(m["msg"], m["value"])
				if nmsgs.Add(1) == 2 { // number depends on logger calls below
					close(done)
				}
			},
		})

	// Connect the server and client.
	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, t1, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ss.Close()
	cs, err := c.Connect(ctx, t2, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	// Set the minimum log level to "info".
	if err := cs.SetLoggingLevel(ctx, &mcp.SetLoggingLevelParams{Level: "info"}); err != nil {
		log.Fatal(err)
	}

	// Get a slog.Logger for the server session.
	logger := slog.New(mcp.NewLoggingHandler(ss, nil))

	// Log some things.
	logger.Info("info shows up", "value", 1)
	logger.Debug("debug doesn't show up", "value", 2)
	logger.Warn("warn shows up", "value", 3)

	// Wait for them to arrive on the client.
	// In a real application, the log messages would appear asynchronously
	// while other work was happening.
	<-done

	// Output:
	// info shows up 1
	// warn shows up 3
}

// !-logging
