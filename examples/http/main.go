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
		serverMode = flag.Bool("server", false, "Run as server")
		clientMode = flag.Bool("client", false, "Run as client")
		host       = flag.String("host", "localhost", "Host to connect to or listen on")
		port       = flag.String("port", "8080", "Port to connect to or listen on")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "This program demonstrates MCP over HTTP using the streamable transport.\n")
		fmt.Fprintf(os.Stderr, "It can run as either a server or client.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  Run as server:  %s -server\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Run as client:  %s -client\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  Custom host/port: %s -server -host 0.0.0.0 -port 9090\n", os.Args[0])
		os.Exit(1)
	}

	flag.Parse()

	if (*serverMode && *clientMode) || (!*serverMode && !*clientMode) {
		fmt.Fprintf(os.Stderr, "Error: Must specify exactly one of -server or -client\n\n")
		flag.Usage()
	}

	if *serverMode {
		runServer(*host, *port)
	} else {
		runClient(*host, *port)
	}
}

// GetTimeParams defines the parameters for the get_time tool
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
		now.Format("3:04:05 PM MST on Monday, January 2, 2006"))

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

	// Add the get_time tool
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_time",
		Description: "Get the current time in NYC, San Francisco, or Boston",
	}, getTime)

	// Create the streamable HTTP handler
	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return server
	}, nil)

	addr := fmt.Sprintf("%s:%s", host, port)
	log.Printf("MCP server listening on http://%s", addr)
	log.Printf("Available tool: get_time (cities: nyc, sf, boston)")

	// Start the HTTP server
	if err := http.ListenAndServe(addr, handler); err != nil {
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
	log.Println("\nListing available tools...")
	toolsResult, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}

	for _, tool := range toolsResult.Tools {
		log.Printf("  - %s: %s", tool.Name, tool.Description)
	}

	// Call the get_time tool for each city
	cities := []string{"nyc", "sf", "boston"}
	
	log.Println("\nGetting time for each city...")
	for _, city := range cities {
		// Call the tool
		result, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "get_time",
			Arguments: map[string]interface{}{
				"city": city,
			},
		})
		if err != nil {
			log.Printf("Failed to get time for %s: %v", city, err)
			continue
		}

		// Print the result
		for _, content := range result.Content {
			if textContent, ok := content.(*mcp.TextContent); ok {
				log.Printf("  %s", textContent.Text)
			}
		}
	}

	log.Println("\nClient completed successfully")
}