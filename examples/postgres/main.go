// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")

func main() {
	flag.Parse()

	// Get database URL from environment variable or command line
	var databaseURL string

	// First try environment variable
	if envURL := os.Getenv("DATABASE_URL"); envURL != "" {
		databaseURL = envURL
		log.Printf("Using DATABASE_URL from environment")
	} else {
		// Fall back to command line argument
		args := os.Args[1:]
		if len(args) == 0 {
			// Default to local development database if not specified
			databaseURL = "postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"
			log.Printf("No DATABASE_URL or command line argument provided, using default: %s", databaseURL)
		} else {
			databaseURL = args[0]
			log.Printf("Using database URL from command line")
		}
	}

	// Create PostgreSQL server
	postgresServer, err := NewPostgresServer(databaseURL)
	if err != nil {
		log.Fatalf("Failed to create PostgreSQL server: %v", err)
	}
	defer postgresServer.Close()

	log.Printf("Connected to PostgreSQL database successfully")

	// Create MCP server
	server := mcp.NewServer("postgres", "0.1.0", nil)

	// Add the query tool
	server.AddTools(mcp.NewServerTool("query", "Run a read-only SQL query", postgresServer.QueryTool, mcp.Input(
		mcp.Property("sql", mcp.Description("The SQL query to execute")),
	)))

	// Get and add resources (tables) dynamically
	ctx := context.Background()
	resources, err := postgresServer.ListTables(ctx)
	if err != nil {
		log.Fatalf("Failed to list database tables: %v", err)
	}
	server.AddResources(resources...)

	// Start server
	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("PostgreSQL MCP server listening at %s", *httpAddr)
		http.ListenAndServe(*httpAddr, handler)
	} else {
		log.Printf("PostgreSQL MCP server running on stdio")
		t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
		if err := server.Run(context.Background(), t); err != nil {
			log.Printf("Server failed: %v", err)
		}
	}
}
