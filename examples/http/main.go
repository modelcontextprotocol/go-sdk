// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	var (
		host = flag.String("host", "localhost", "Host to connect to or listen on")
		port = flag.String("port", "8080", "Port to connect to or listen on")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <client|server> [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "This program demonstrates MCP over HTTP using the streamable transport.\n")
		fmt.Fprintf(os.Stderr, "It can run as either a server or client.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  Run as server:  %s server\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Run as client:  %s client\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Custom host/port: %s server -host 0.0.0.0 -port 9090\n", os.Args[0])
		os.Exit(1)
	}

	// Check if we have at least one argument
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Error: Must specify 'client' or 'server' as first argument\n\n")
		flag.Usage()
	}
	mode := os.Args[1]

	os.Args = append(os.Args[:1], os.Args[2:]...)
	flag.Parse()
    
	switch mode {
	case "server":
		runServer(*host, *port)
	case "client":
		runClient(*host, *port)
	default:
		fmt.Fprintf(os.Stderr, "Error: Invalid mode '%s'. Must be 'client' or 'server'\n\n", mode)
		flag.Usage()
	}
}

// GetTimeParams defines the parameters for the cityTime tool
type GetTimeParams struct {
	City string `json:"city" jsonschema:"City to get time for (nyc, sf, or boston)"`
}

// getTime implements the tool that returns the current time for a given city
func getTime(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[GetTimeParams]) (*mcp.CallToolResultFor[any], error) {
	// Define time zones for each city
	locations := map[string]string{
		"nyc":    "America/New_York",
		"sf":     "America/Los_Angeles",
		"boston": "America/New_York",
	}

	city := params.Arguments.City
	if city == "" {
		city = "nyc" // Default to NYC
	}

	// Get the timezone
	tzName, ok := locations[city]
	if !ok {
		return nil, fmt.Errorf("unknown city: %s", city)
	}

	// Load the location
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("failed to load timezone: %w", err)
	}

	// Get current time in that location
	now := time.Now().In(loc)

	// Format the response
	cityNames := map[string]string{
		"nyc":    "New York City",
		"sf":     "San Francisco",
		"boston": "Boston",
	}

	response := fmt.Sprintf("The current time in %s is %s", 
		cityNames[city], 
		now.Format(time.RFC3339))

	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: response},
		},
	}, nil
}

func runServer(host, port string) {
	// Create an MCP server
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "time-server",
		Version: "1.0.0",
	}, nil)

	// Add the cityTime tool
	mcp.AddTool(server, &mcp.Tool{
		Name:        "cityTime",
		Description: "Get the current time in NYC, San Francisco, or Boston",
	}, getTime)

	// Create the streamable HTTP handler
	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return server
	}, nil)

	handlerWithLogging := loggingHandler(handler)

	addr := fmt.Sprintf("%s:%s", host, port)
	log.Printf("MCP server listening on http://%s", addr)
	log.Printf("Available tool: cityTime (cities: nyc, sf, boston)")

	// Start the HTTP server with logging handler
	if err := http.ListenAndServe(addr, handlerWithLogging); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func runClient(host, port string) {
	ctx := context.Background()
	
	// Create the URL for the server
	url := fmt.Sprintf("http://%s:%s", host, port)
	log.Printf("Connecting to MCP server at %s", url)

	// Create a streamable client transport
	transport := mcp.NewStreamableClientTransport(url, nil)

	// Create an MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "time-client",
		Version: "1.0.0",
	}, nil)

	// Connect to the server
	session, err := client.Connect(ctx, transport)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer session.Close()

	log.Printf("Connected to server (session ID: %s)", session.ID())

	// First, list available tools
	log.Println("Listing available tools...")
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}

	for _, tool := range toolsResult.Tools {
		log.Printf("  - %s: %s\n", tool.Name, tool.Description)
	}

	// Call the cityTime tool for each city
	cities := []string{"nyc", "sf", "boston"}
	
	log.Println("Getting time for each city...")
	for _, city := range cities {
		// Call the tool
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "cityTime",
			Arguments: map[string]any{
				"city": city,
			},
		})
		if err != nil {
			log.Printf("Failed to get time for %s: %v\n", city, err)
			continue
		}

		// Print the result
		for _, content := range result.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				log.Printf("  %s", textContent.Text)
			}
		}
	}

	log.Println("Client completed successfully")
}
