// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// !+loggingtransport

func ExampleLoggingTransport() {
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	serverSession, err := server.Connect(ctx, t1, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer serverSession.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	var b bytes.Buffer
	logTransport := &mcp.LoggingTransport{Transport: t2, Writer: &b}
	clientSession, err := client.Connect(ctx, logTransport, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer clientSession.Close()

	// Sort for stability: reads are concurrent to writes.
	for _, line := range slices.Sorted(strings.SplitSeq(b.String(), "\n")) {
		fmt.Println(line)
	}

	// Output:
	// read: {"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found: \"server/discover\""}}
	// read: {"jsonrpc":"2.0","id":2,"result":{"capabilities":{"logging":{}},"protocolVersion":"2025-11-25","serverInfo":{"name":"server","version":"v0.0.1"}}}
	// write: {"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{"roots":{"listChanged":true}},"io.modelcontextprotocol/clientInfo":{"name":"client","version":"v0.0.1"},"io.modelcontextprotocol/protocolVersion":"2026-06-30"}}}
	// write: {"jsonrpc":"2.0","id":2,"method":"initialize","params":{"clientInfo":{"name":"client","version":"v0.0.1"},"protocolVersion":"2025-11-25","capabilities":{"roots":{"listChanged":true}}}}
	// write: {"jsonrpc":"2.0","method":"notifications/initialized","params":{}}
}

// !-loggingtransport
