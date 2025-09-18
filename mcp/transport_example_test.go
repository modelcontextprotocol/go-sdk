// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Uses strings.SplitSeq.
//go:build go1.24

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
	defer serverSession.Wait()

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
	// read: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{}},"protocolVersion":"2025-06-18","serverInfo":{"name":"server","version":"v0.0.1"}}}
	// write: {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"client","version":"v0.0.1"},"protocolVersion":"2025-06-18"}}
	// write: {"jsonrpc":"2.0","method":"notifications/initialized","params":{}}
}

// !-loggingtransport
