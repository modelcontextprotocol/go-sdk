// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// The custom-method example demonstrates the extension-author / extension-consumer
// split for custom JSON-RPC methods.
//
// The latinext sub-package is the "extension author": it defines the types,
// registers the method via init(), and exposes a domain-specific Translate()
// helper. This file is the "extension consumer": importing latinext is all
// that's needed to wire up both sides.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/examples/server/custom-method/latinext"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()

	// NewServer and NewClient automatically apply all extensions registered via
	// init() — including latinext's handler and sending registration.
	server := mcp.NewServer(&mcp.Implementation{Name: "latin-server", Version: "v1.0.0"}, nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "latin-client", Version: "v1.0.0"}, nil)

	ct, st := mcp.NewInMemoryTransports()

	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ss.Close()

	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	// Call the custom method — no generics, no method-name strings.
	phrases := []string{"Hello", "Seize the day", "Peace", "Truth", "I came I saw I conquered"}
	for _, phrase := range phrases {
		result, err := latinext.Translate(ctx, cs, phrase)
		if err != nil {
			log.Fatalf("translate %q: %v", phrase, err)
		}
		fmt.Printf("%-35s → %s\n", phrase, result.Latin)
	}
}
