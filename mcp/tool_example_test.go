// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ExampleAddTool_customTypeSchema() {
	// Sometimes when you want to customize the input or output schema for a
	// tool, you need to customize the schema of a single helper type that's used
	// in several places.
	//
	// For example, suppose you had a type that marshals/unmarshals like a
	// time.Time, and that type was used multiple times in your tool input.
	type MyDate struct {
		time.Time
	}
	type Input struct {
		Query string `json:"query,omitempty"`
		Start MyDate `json:"start,omitempty"`
		End   MyDate `json:"end,omitempty"`
	}

	// In this case, you can use jsonschema.For along with jsonschema.ForOptions
	// to customize the schema inference for your custom type.
	inputSchema, err := jsonschema.For[Input](&jsonschema.ForOptions{
		TypeSchemas: map[any]*jsonschema.Schema{
			MyDate{}: {Type: "string"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	toolHandler := func(context.Context, *mcp.CallToolRequest, Input) (*mcp.CallToolResult, any, error) {
		panic("not implemented")
	}
	mcp.AddTool(server, &mcp.Tool{Name: "my_tool", InputSchema: inputSchema}, toolHandler)

	ctx := context.Background()
	session, err := connect(ctx, server) // create an in-memory connection
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		schemaJSON, err := json.MarshalIndent(t.InputSchema, "", "\t")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(t.Name, string(schemaJSON))
	}
	// Output:
	// my_tool {
	// 	"type": "object",
	// 	"properties": {
	// 		"end": {
	// 			"type": "string"
	// 		},
	// 		"query": {
	// 			"type": "string"
	// 		},
	// 		"start": {
	// 			"type": "string"
	// 		}
	// 	},
	// 	"additionalProperties": false
	// }
}

func connect(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	return client.Connect(ctx, t2, nil)
}
