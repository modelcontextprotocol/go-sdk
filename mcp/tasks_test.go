// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestHasExtension(t *testing.T) {
	tests := []struct {
		name       string
		extensions map[string]any
		lookup     string
		want       bool
	}{
		{"nil map", nil, ExtensionTasks, false},
		{"empty map", map[string]any{}, ExtensionTasks, false},
		{"absent", map[string]any{"io.example/other": map[string]any{}}, ExtensionTasks, false},
		{"present, empty settings", map[string]any{ExtensionTasks: map[string]any{}}, ExtensionTasks, true},
		{"present, with settings", map[string]any{ExtensionTasks: map[string]any{"k": "v"}}, ExtensionTasks, true},
		{"present, nil settings", map[string]any{ExtensionTasks: nil}, ExtensionTasks, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &ClientCapabilities{Extensions: tc.extensions}
			if got := client.HasExtension(tc.lookup); got != tc.want {
				t.Errorf("ClientCapabilities.HasExtension(%q) = %v, want %v", tc.lookup, got, tc.want)
			}
			server := &ServerCapabilities{Extensions: tc.extensions}
			if got := server.HasExtension(tc.lookup); got != tc.want {
				t.Errorf("ServerCapabilities.HasExtension(%q) = %v, want %v", tc.lookup, got, tc.want)
			}
		})
	}

	t.Run("nil receiver", func(t *testing.T) {
		var client *ClientCapabilities
		if client.HasExtension(ExtensionTasks) {
			t.Error("(*ClientCapabilities)(nil).HasExtension = true, want false")
		}
		var server *ServerCapabilities
		if server.HasExtension(ExtensionTasks) {
			t.Error("(*ServerCapabilities)(nil).HasExtension = true, want false")
		}
	})

	t.Run("round trip with AddExtension", func(t *testing.T) {
		client := new(ClientCapabilities)
		client.AddExtension(ExtensionTasks, nil)
		if !client.HasExtension(ExtensionTasks) {
			t.Error("ClientCapabilities.HasExtension after AddExtension = false, want true")
		}
		server := new(ServerCapabilities)
		server.AddExtension(ExtensionTasks, nil)
		if !server.HasExtension(ExtensionTasks) {
			t.Error("ServerCapabilities.HasExtension after AddExtension = false, want true")
		}
	})
}

// TestServerSeesClientTasksExtension checks that a client declaring the tasks
// extension is visible to a server request handler, across both capability
// transports: the per-request _meta of protocol 2026-07-28, and the
// initialize handshake of older versions.
//
// The declared=false cases also pin that the SDK never declares the extension
// on the user's behalf, since it does not implement task execution.
func TestServerSeesClientTasksExtension(t *testing.T) {
	for _, version := range []string{protocolVersion20260728, protocolVersion20251125} {
		t.Run(version, func(t *testing.T) {
			for _, declare := range []bool{true, false} {
				t.Run(fmt.Sprintf("declared=%t", declare), func(t *testing.T) {
					ctx := context.Background()

					var got atomic.Bool
					server := NewServer(testImpl, nil)
					server.AddTool(
						&Tool{Name: "probe", InputSchema: &jsonschema.Schema{Type: "object"}},
						func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
							got.Store(req.ClientCapabilities().HasExtension(ExtensionTasks))
							return &CallToolResult{Content: []Content{&TextContent{Text: "ok"}}}, nil
						})

					clientOpts := new(ClientOptions)
					if declare {
						caps := new(ClientCapabilities)
						caps.AddExtension(ExtensionTasks, nil)
						clientOpts.Capabilities = caps
					}

					ct, st := NewInMemoryTransports()
					ss, err := server.Connect(ctx, st, nil)
					if err != nil {
						t.Fatalf("server Connect: %v", err)
					}
					defer ss.Close()

					cs, err := NewClient(testImpl, clientOpts).Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: version})
					if err != nil {
						t.Fatalf("client Connect: %v", err)
					}
					defer cs.Close()

					if _, err := cs.CallTool(ctx, &CallToolParams{Name: "probe"}); err != nil {
						t.Fatalf("CallTool: %v", err)
					}
					if got.Load() != declare {
						t.Errorf("handler saw %s = %v, want %v", ExtensionTasks, got.Load(), declare)
					}
				})
			}
		})
	}
}

// TestClientSeesServerTasksExtension checks that a server declaring the tasks
// extension is visible to the client, both through server/discover and through
// the legacy initialize handshake.
func TestClientSeesServerTasksExtension(t *testing.T) {
	for _, version := range []string{protocolVersion20260728, protocolVersion20251125} {
		t.Run(version, func(t *testing.T) {
			for _, declare := range []bool{true, false} {
				t.Run(fmt.Sprintf("declared=%t", declare), func(t *testing.T) {
					ctx := context.Background()

					serverOpts := new(ServerOptions)
					if declare {
						caps := new(ServerCapabilities)
						caps.AddExtension(ExtensionTasks, nil)
						serverOpts.Capabilities = caps
					}

					ct, st := NewInMemoryTransports()
					ss, err := NewServer(testImpl, serverOpts).Connect(ctx, st, nil)
					if err != nil {
						t.Fatalf("server Connect: %v", err)
					}
					defer ss.Close()

					cs, err := NewClient(testImpl, nil).Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: version})
					if err != nil {
						t.Fatalf("client Connect: %v", err)
					}
					defer cs.Close()

					if got := cs.InitializeResult().Capabilities.HasExtension(ExtensionTasks); got != declare {
						t.Errorf("client saw %s = %v, want %v", ExtensionTasks, got, declare)
					}
				})
			}
		})
	}
}

// TestUnmarshalTaskResult checks that a CreateTaskResult from the tasks
// extension is rejected rather than silently decoding into an empty result.
func TestUnmarshalTaskResult(t *testing.T) {
	const taskResult = `{
		"resultType": "task",
		"taskId": "786512e2",
		"status": "working",
		"createdAt": "2026-01-01T00:00:00Z",
		"lastUpdatedAt": "2026-01-01T00:00:00Z",
		"ttlMs": 60000,
		"pollIntervalMs": 5000
	}`

	targets := []struct {
		name      string
		newTarget func() any
	}{
		{"CallToolResult", func() any { return new(CallToolResult) }},
		{"GetPromptResult", func() any { return new(GetPromptResult) }},
		{"ReadResourceResult", func() any { return new(ReadResourceResult) }},
	}

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			t.Run("task is rejected", func(t *testing.T) {
				err := json.Unmarshal([]byte(taskResult), target.newTarget())
				var terr *UnsupportedTaskResultError
				if !errors.As(err, &terr) {
					t.Fatalf("Unmarshal error = %v, want *UnsupportedTaskResultError", err)
				}
				if got, want := terr.TaskID, "786512e2"; got != want {
					t.Errorf("TaskID = %q, want %q", got, want)
				}
			})

			t.Run("complete still decodes", func(t *testing.T) {
				if err := json.Unmarshal([]byte(`{"resultType":"complete"}`), target.newTarget()); err != nil {
					t.Errorf("Unmarshal of a complete result failed: %v", err)
				}
			})
		})
	}
}

// taskResultStub is a [Result] that marshals to a CreateTaskResult, standing in
// for a server that has decided to answer a request with a task handle.
type taskResultStub struct {
	ResultBase
	taskID string
}

func (s *taskResultStub) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"resultType":     "task",
		"taskId":         s.taskID,
		"status":         "working",
		"createdAt":      "2026-01-01T00:00:00Z",
		"lastUpdatedAt":  "2026-01-01T00:00:00Z",
		"ttlMs":          60000,
		"pollIntervalMs": 5000,
	})
}

// TestCallToolTaskResultEndToEnd checks that the decode guard survives the real
// client call path with its error identity intact, rather than surfacing as an
// empty but successful tool result.
func TestCallToolTaskResultEndToEnd(t *testing.T) {
	ctx := context.Background()

	server := NewServer(testImpl, nil)
	server.AddTool(
		&Tool{Name: "probe", InputSchema: &jsonschema.Schema{Type: "object"}},
		func(ctx context.Context, req *CallToolRequest) (*CallToolResult, error) {
			return nil, errors.New("unreachable: intercepted by middleware")
		})
	server.AddReceivingMiddleware(func(next MethodHandler) MethodHandler {
		return func(ctx context.Context, method string, req Request) (Result, error) {
			if method == methodCallTool {
				return &taskResultStub{taskID: "786512e2"}, nil
			}
			return next(ctx, method, req)
		}
	})

	ct, st := NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	defer ss.Close()

	cs, err := NewClient(testImpl, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &CallToolParams{Name: "probe"})
	if err == nil {
		t.Fatalf("CallTool succeeded with %+v, want an error", res)
	}
	var terr *UnsupportedTaskResultError
	if !errors.As(err, &terr) {
		t.Fatalf("CallTool error = %v, want *UnsupportedTaskResultError", err)
	}
	if got, want := terr.TaskID, "786512e2"; got != want {
		t.Errorf("TaskID = %q, want %q", got, want)
	}
}
