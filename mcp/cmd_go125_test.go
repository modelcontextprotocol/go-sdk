// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build go1.25

package mcp_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestServerRunContextCancel_Synctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := mcp.NewServer(&mcp.Implementation{Name: "greeter", Version: "v0.0.1"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "say hi"}, SayHi)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		serverTransport, clientTransport := mcp.NewInMemoryTransports()

		// run the server and capture the exit error
		onServerExit := make(chan error)
		go func() {
			onServerExit <- server.Run(ctx, serverTransport)
		}()

		// send a ping to the server to ensure it's running
		client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
		session, err := client.Connect(ctx, clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })

		if err := session.Ping(context.Background(), nil); err != nil {
			t.Fatal(err)
		}

		// cancel the context to stop the server
		cancel()

		// wait for the server to exit

		err = <-onServerExit
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("server did not exit after context cancellation, got error: %v", err)
		}
	})
}
