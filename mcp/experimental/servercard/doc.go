// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package servercard builds and serves MCP Server Cards from the experimental
// Server Card extension proposed by SEP-2127.
//
// Server Cards are static JSON documents that describe a remote MCP server's
// identity and connection details for pre-connection discovery. They do not
// describe the server's tools, resources, or prompts; clients must use the live
// MCP session as the authoritative source for capabilities and access.
//
// This package is experimental. Its API and the wire format may change or be
// removed without deprecation as SEP-2127 evolves. Server Cards are normally
// public, so they must not contain credentials or private network topology.
//
// A typical server builds a card from its MCP implementation metadata and serves
// it near its Streamable HTTP endpoint:
//
//	impl := &mcp.Implementation{
//		Name:    "com.example/dice-roller",
//		Title:   "Dice Roller",
//		Version: "1.0.0",
//	}
//	card, err := servercard.BuildServerCard(impl,
//		servercard.WithName("com.example/dice-roller"),
//		servercard.WithDescription("Rolls dice for tabletop games."),
//		servercard.WithRemotes(servercard.Remote{
//			Type: servercard.RemoteTypeStreamableHTTP,
//			URL:  "https://dice.example.com/mcp",
//		}),
//	)
//	if err != nil {
//		// handle error
//	}
//	mux.Handle("/mcp/server-card", servercard.Handler(card))
//
// [SEP-2127]: https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2127
package servercard
