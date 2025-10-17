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
	"net/http"
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
	// read: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{}},"protocolVersion":"2025-06-18","serverInfo":{"name":"server","version":"v0.0.1"}}}
	// write: {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}},"clientInfo":{"name":"client","version":"v0.0.1"},"protocolVersion":"2025-06-18"}}
	// write: {"jsonrpc":"2.0","method":"notifications/initialized","params":{}}
}

// !-loggingtransport

func ExampleStreamableClientTransport_ModifyRequest() {
	// Create a transport with ModifyRequest to add authentication headers
	transport := &mcp.StreamableClientTransport{
		Endpoint: "https://example.com/mcp",
		ModifyRequest: func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer my-secret-token")
			req.Header.Set("X-Request-ID", "req-12345")
		},
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "my-client", Version: "v1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// All HTTP requests (POST, GET, DELETE) will have the custom headers
	// added by ModifyRequest before being sent to the server.
}

// This example demonstrates how to use ModifyRequest with SSEClientTransport.
func ExampleSSEClientTransport_ModifyRequest() {
	// Create a transport with ModifyRequest
	transport := &mcp.SSEClientTransport{
		Endpoint: "https://example.com/sse",
		ModifyRequest: func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer my-token")
			req.Header.Set("X-Custom-Header", "custom-value")
		},
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "my-client", Version: "v1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// All HTTP requests will have the custom headers
}
