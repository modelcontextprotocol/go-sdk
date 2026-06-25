// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package servercard builds and serves MCP Server Cards (SEP-2127).
//
// Server Cards are static JSON documents that describe a remote MCP server's
// identity and connection details for pre-connection discovery. They are
// experimental and may change as SEP-2127 evolves.
//
// A typical server builds a card from its MCP implementation metadata and serves
// it near its Streamable HTTP endpoint:
//
//	impl := &mcp.Implementation{
//		Name:        "dice-roller",
//		Title:       "Dice Roller",
//		Description: "Rolls dice for tabletop games.",
//		Version:     "1.0.0",
//	}
//	card, err := servercard.BuildServerCard(impl,
//		servercard.WithName("com.example/dice-roller"),
//		servercard.WithRemotes(servercard.Remote{
//			Type: servercard.RemoteTypeStreamableHTTP,
//			URL:  "https://dice.example.com/mcp",
//		}),
//	)
//	if err != nil {
//		// handle error
//	}
//	mux.Handle("/mcp/server-card", servercard.Handler(card))
package servercard
