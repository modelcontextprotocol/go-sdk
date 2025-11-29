package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	rootDir := "."
	if len(os.Args) > 1 {
		rootDir = os.Args[1]
	}

	fmt.Printf("Starting Vibecoder Server in %s...\n", rootDir)

	// Create server
	server, err := mcp.NewServer(rootDir)
	if err != nil {
		log.Fatal(err)
	}

	// Run server
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
