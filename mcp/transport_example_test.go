// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// !+loggingtransport

func ExampleLoggingTransport() {
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		log.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	var b bytes.Buffer
	logTransport := &mcp.LoggingTransport{Transport: t2, Writer: &b}
	if _, err := client.Connect(ctx, logTransport, nil); err != nil {
		log.Fatal(err)
	}
	fmt.Println(b.String())
	// Output:
	// write: {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"client","version":"v0.0.1"},"protocolVersion":"2025-06-18"}}
	// read: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{}},"protocolVersion":"2025-06-18","serverInfo":{"name":"server","version":"v0.0.1"}}}
	// write: {"jsonrpc":"2.0","method":"notifications/initialized","params":{}}

}

// !-loggingtransport
