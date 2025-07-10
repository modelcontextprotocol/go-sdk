// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const runAsServer = "_MCP_RUN_AS_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(runAsServer) != "" {
		os.Unsetenv(runAsServer)
		runServer()
		return
	}
	os.Exit(m.Run())
}

func runServer() {
	ctx := context.Background()

	server := mcp.NewServer(testImpl, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "greet", Description: "say hi"}, SayHi)
	if err := server.Run(ctx, mcp.NewStdioTransport()); err != nil {
		log.Fatal(err)
	}
}

func TestServerRunContextCancel(t *testing.T) {
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
	session, err := client.Connect(ctx, clientTransport)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Ping(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	// cancel the context to stop the server
	cancel()

	// wait for the server to exit
	// TODO: use synctest when availble
	select {
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after context cancellation")
	case err := <-onServerExit:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("server did not exit after context cancellation, got error: %v", err)
		}
	}
}

func TestServerInterrupt(t *testing.T) {
	requireExec(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := createServerCommand(t)

	client := mcp.NewClient(testImpl, nil)
	session, err := client.Connect(ctx, mcp.NewCommandTransport(cmd))
	if err != nil {
		t.Fatal(err)
	}

	// get a signal when the server process exits
	onExit := make(chan struct{})
	go func() {
		cmd.Process.Wait()
		close(onExit)
	}()

	// send a signal to the server process to terminate it
	if runtime.GOOS == "windows" {
		// Windows does not support os.Interrupt
		session.Close()
	} else {
		cmd.Process.Signal(os.Interrupt)
	}

	// wait for the server to exit
	// TODO: use synctest when availble
	select {
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after SIGTERM")
	case <-onExit:
	}
}

func TestCmdTransport(t *testing.T) {
	requireExec(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := createServerCommand(t)

	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, mcp.NewCommandTransport(cmd))
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "user"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Hi user"},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("greet returned unexpected content (-want +got):\n%s", diff)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("closing server: %v", err)
	}
}

func createServerCommand(t *testing.T) *exec.Cmd {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), runAsServer+"=true")

	return cmd
}

func requireExec(t *testing.T) {
	t.Helper()

	// Conservatively, limit to major OS where we know that os.Exec is
	// supported.
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("unsupported OS")
	}
}

var testImpl = &mcp.Implementation{Name: "test", Version: "v1.0.0"}
