// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestToolTasksBasicLifecycle(t *testing.T) {
	ctx := context.Background()

	// Enable task support for tools/call.
	server := mcp.NewServer(&mcp.Implementation{Name: "testServer", Version: "v1.0.0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tasks: &mcp.TasksCapabilities{
				List:   &mcp.TasksListCapabilities{},
				Cancel: &mcp.TasksCancelCapabilities{},
				Requests: &mcp.TasksRequestsCapabilities{
					Tools: &mcp.TasksToolsRequestCapabilities{Call: &mcp.TasksToolsCallCapabilities{}},
				},
			},
		},
	})

	start := make(chan struct{})
	server.AddTool(&mcp.Tool{
		Name:       "slow",
		InputSchema: &jsonschema.Schema{Type: "object"},
		Execution:  &mcp.ToolExecution{TaskSupport: mcp.ToolTaskSupportOptional},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-start:
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	cTransport, sTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "testClient", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ttl := int64(60_000)
	createRes, err := cs.CallToolTask(ctx, &mcp.CallToolParams{
		Name:      "slow",
		Arguments: map[string]any{},
		Task:      &mcp.TaskParams{TTL: &ttl},
	})
	if err != nil {
		t.Fatalf("CallToolTask failed: %v", err)
	}
	if createRes.Task == nil || createRes.Task.TaskID == "" {
		t.Fatalf("CreateTaskResult missing task/taskId: %#v", createRes)
	}
	if got, want := createRes.Task.Status, mcp.TaskStatusWorking; got != want {
		t.Fatalf("initial status: got %q want %q", got, want)
	}

	close(start)

	// TaskResult should block until completion and then return the tool result.
	resultCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	toolRes, err := cs.TaskResult(resultCtx, &mcp.TaskResultParams{TaskID: createRes.Task.TaskID})
	if err != nil {
		t.Fatalf("TaskResult failed: %v", err)
	}
	if toolRes == nil || len(toolRes.Content) != 1 {
		t.Fatalf("unexpected tool result: %#v", toolRes)
	}

	getRes, err := cs.GetTask(ctx, &mcp.GetTaskParams{TaskID: createRes.Task.TaskID})
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if got, want := getRes.Status, mcp.TaskStatusCompleted; got != want {
		t.Fatalf("final status: got %q want %q", got, want)
	}
}

func TestToolTasksCancel(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "testServer", Version: "v1.0.0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tasks: &mcp.TasksCapabilities{
				List:   &mcp.TasksListCapabilities{},
				Cancel: &mcp.TasksCancelCapabilities{},
				Requests: &mcp.TasksRequestsCapabilities{
					Tools: &mcp.TasksToolsRequestCapabilities{Call: &mcp.TasksToolsCallCapabilities{}},
				},
			},
		},
	})

	block := make(chan struct{})
	server.AddTool(&mcp.Tool{
		Name:       "block",
		InputSchema: &jsonschema.Schema{Type: "object"},
		Execution:  &mcp.ToolExecution{TaskSupport: mcp.ToolTaskSupportOptional},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-block:
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	cTransport, sTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "testClient", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ttl := int64(60_000)
	createRes, err := cs.CallToolTask(ctx, &mcp.CallToolParams{
		Name:      "block",
		Arguments: map[string]any{},
		Task:      &mcp.TaskParams{TTL: &ttl},
	})
	if err != nil {
		t.Fatalf("CallToolTask failed: %v", err)
	}

	cancelRes, err := cs.CancelTask(ctx, &mcp.CancelTaskParams{TaskID: createRes.Task.TaskID})
	if err != nil {
		t.Fatalf("CancelTask failed: %v", err)
	}
	if got, want := cancelRes.Status, mcp.TaskStatusCancelled; got != want {
		t.Fatalf("cancel status: got %q want %q", got, want)
	}

	resultCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = cs.TaskResult(resultCtx, &mcp.TaskResultParams{TaskID: createRes.Task.TaskID})
	if err == nil {
		t.Fatalf("TaskResult unexpectedly succeeded after cancel")
	}
}

func TestTasksListPaginationAndCursor(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "testServer", Version: "v1.0.0"}, &mcp.ServerOptions{
		PageSize: 1,
		Capabilities: &mcp.ServerCapabilities{
			Tasks: &mcp.TasksCapabilities{
				List:   &mcp.TasksListCapabilities{},
				Cancel: &mcp.TasksCancelCapabilities{},
				Requests: &mcp.TasksRequestsCapabilities{
					Tools: &mcp.TasksToolsRequestCapabilities{Call: &mcp.TasksToolsCallCapabilities{}},
				},
			},
		},
	})

	block := make(chan struct{})
	server.AddTool(&mcp.Tool{
		Name:       "block",
		InputSchema: &jsonschema.Schema{Type: "object"},
		Execution:  &mcp.ToolExecution{TaskSupport: mcp.ToolTaskSupportOptional},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		select {
		case <-block:
			return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	cTransport, sTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "testClient", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ttl := int64(60_000)
	create1, err := cs.CallToolTask(ctx, &mcp.CallToolParams{Name: "block", Arguments: map[string]any{}, Task: &mcp.TaskParams{TTL: &ttl}})
	if err != nil {
		t.Fatalf("CallToolTask #1 failed: %v", err)
	}
	create2, err := cs.CallToolTask(ctx, &mcp.CallToolParams{Name: "block", Arguments: map[string]any{}, Task: &mcp.TaskParams{TTL: &ttl}})
	if err != nil {
		t.Fatalf("CallToolTask #2 failed: %v", err)
	}
	if create1.Task.TaskID == create2.Task.TaskID {
		t.Fatalf("expected distinct task IDs")
	}

	page1, err := cs.ListTasks(ctx, &mcp.ListTasksParams{})
	if err != nil {
		t.Fatalf("ListTasks page1 failed: %v", err)
	}
	if got := len(page1.Tasks); got != 1 {
		t.Fatalf("ListTasks page1: got %d tasks, want 1", got)
	}
	if page1.NextCursor == "" {
		t.Fatalf("ListTasks page1: expected nextCursor")
	}

	page2, err := cs.ListTasks(ctx, &mcp.ListTasksParams{Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("ListTasks page2 failed: %v", err)
	}
	if got := len(page2.Tasks); got != 1 {
		t.Fatalf("ListTasks page2: got %d tasks, want 1", got)
	}
	if page2.NextCursor != "" {
		t.Fatalf("ListTasks page2: expected empty nextCursor, got %q", page2.NextCursor)
	}

	_, err = cs.ListTasks(ctx, &mcp.ListTasksParams{Cursor: "999999999"})
	if err == nil {
		t.Fatalf("ListTasks with invalid cursor unexpectedly succeeded")
	}
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("ListTasks invalid cursor: got %T/%v, want jsonrpc invalid params", err, err)
	}

	close(block)
	_, _ = cs.TaskResult(ctx, &mcp.TaskResultParams{TaskID: create1.Task.TaskID})
	_, _ = cs.TaskResult(ctx, &mcp.TaskResultParams{TaskID: create2.Task.TaskID})
}

func TestTasksGetNotFound(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "testServer", Version: "v1.0.0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tasks: &mcp.TasksCapabilities{}},
	})

	cTransport, sTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "testClient", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	_, err = cs.GetTask(ctx, &mcp.GetTaskParams{TaskID: "does-not-exist"})
	if err == nil {
		t.Fatalf("GetTask unexpectedly succeeded")
	}
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("GetTask not found: got %T/%v, want jsonrpc invalid params", err, err)
	}
}

func TestTasksCancelTerminalRejected(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "testServer", Version: "v1.0.0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tasks: &mcp.TasksCapabilities{
				Cancel: &mcp.TasksCancelCapabilities{},
				Requests: &mcp.TasksRequestsCapabilities{Tools: &mcp.TasksToolsRequestCapabilities{Call: &mcp.TasksToolsCallCapabilities{}}},
			},
		},
	})

	start := make(chan struct{})
	server.AddTool(&mcp.Tool{
		Name:       "finish",
		InputSchema: &jsonschema.Schema{Type: "object"},
		Execution:  &mcp.ToolExecution{TaskSupport: mcp.ToolTaskSupportOptional},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-start
		return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
	})

	cTransport, sTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "testClient", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ttl := int64(60_000)
	createRes, err := cs.CallToolTask(ctx, &mcp.CallToolParams{Name: "finish", Arguments: map[string]any{}, Task: &mcp.TaskParams{TTL: &ttl}})
	if err != nil {
		t.Fatalf("CallToolTask failed: %v", err)
	}

	close(start)
	resultCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = cs.TaskResult(resultCtx, &mcp.TaskResultParams{TaskID: createRes.Task.TaskID})
	if err != nil {
		t.Fatalf("TaskResult failed: %v", err)
	}

	_, err = cs.CancelTask(ctx, &mcp.CancelTaskParams{TaskID: createRes.Task.TaskID})
	if err == nil {
		t.Fatalf("CancelTask unexpectedly succeeded on terminal task")
	}
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("CancelTask terminal: got %T/%v, want jsonrpc invalid params", err, err)
	}
}

func TestTasksResultIncludesRelatedTaskMeta(t *testing.T) {
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "testServer", Version: "v1.0.0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tasks: &mcp.TasksCapabilities{
				Requests: &mcp.TasksRequestsCapabilities{Tools: &mcp.TasksToolsRequestCapabilities{Call: &mcp.TasksToolsCallCapabilities{}}},
			},
		},
	})

	start := make(chan struct{})
	server.AddTool(&mcp.Tool{
		Name:       "slow",
		InputSchema: &jsonschema.Schema{Type: "object"},
		Execution:  &mcp.ToolExecution{TaskSupport: mcp.ToolTaskSupportOptional},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-start
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})

	cTransport, sTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "testClient", Version: "v1.0.0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ttl := int64(60_000)
	createRes, err := cs.CallToolTask(ctx, &mcp.CallToolParams{Name: "slow", Arguments: map[string]any{}, Task: &mcp.TaskParams{TTL: &ttl}})
	if err != nil {
		t.Fatalf("CallToolTask failed: %v", err)
	}

	close(start)
	resultCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	toolRes, err := cs.TaskResult(resultCtx, &mcp.TaskResultParams{TaskID: createRes.Task.TaskID})
	if err != nil {
		t.Fatalf("TaskResult failed: %v", err)
	}
	meta := toolRes.GetMeta()
	if meta == nil {
		t.Fatalf("TaskResult missing _meta")
	}
	related, ok := meta["io.modelcontextprotocol/related-task"].(map[string]any)
	if !ok {
		t.Fatalf("TaskResult missing related-task metadata: %#v", meta)
	}
	if got, _ := related["taskId"].(string); got != createRes.Task.TaskID {
		t.Fatalf("related-task.taskId: got %q want %q", got, createRes.Task.TaskID)
	}
}
