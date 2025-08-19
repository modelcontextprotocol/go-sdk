// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// The listfeatures command lists all features of a stdio MCP server.
//
// Usage: listfeatures <command> [<args>]
//
// For example:
//
//	listfeatures go run github.com/modelcontextprotocol/go-sdk/examples/server/hello
//
// or
//
//	listfeatures npx @modelcontextprotocol/server-everything
package main

import (
	"context"
	"flag"
	"fmt"
	"iter"
	"log"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: listfeatures <command> [<args>]")
		fmt.Fprintf(os.Stderr, "List all features for a stdio MCP server")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Example: listfeatures npx @modelcontextprotocol/server-everything")
		os.Exit(2)
	}

	ctx := context.Background()
	cmd := exec.Command(args[0], args[1:]...)
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-client", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	printSection("tools", cs.Tools(ctx, nil), func(t *mcp.Tool) string { return t.Name })
	printSection("resources", cs.Resources(ctx, nil), func(r *mcp.Resource) string { return r.Name })
	printSection("resource templates", cs.ResourceTemplates(ctx, nil), func(r *mcp.ResourceTemplate) string { return r.Name })
	printSection("prompts", cs.Prompts(ctx, nil), func(p *mcp.Prompt) string { return p.Name })
}

func printSection[T any](name string, features iter.Seq2[T, error], featName func(T) string) {
	fmt.Printf("%s:\n", name)
	for feat, err := range features {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\t%s\n", featName(feat))
	}
	fmt.Println()
}
