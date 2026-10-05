// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// !+roots

func Example_roots() {
	ctx := context.Background()

	// Create a client with two roots.
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	c.AddRoots(&mcp.Root{URI: "file://a"}, &mcp.Root{URI: "file://b"})

	// Create a server with a tool that requests roots via the multi round-trip
	// pattern (SEP-2322): server-to-client requests are no longer sent as
	// standalone JSON-RPC calls on protocol version >= 2026-07-28.
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "roots"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if len(req.Params.InputResponses) == 0 {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{"roots": &mcp.ListRootsParams{}},
			}, nil, nil
		}
		rootList := req.Params.InputResponses["roots"].(*mcp.ListRootsResult)
		var roots []string
		for _, root := range rootList.Roots {
			roots = append(roots, root.URI)
		}
		fmt.Println(roots)
		return &mcp.CallToolResult{}, nil, nil
	})

	// Connect the server and client...
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := s.Connect(ctx, t1, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer serverSession.Close()

	clientSession, err := c.Connect(ctx, t2, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer clientSession.Close()

	// ...and call the tool. The client's multi round-trip driver fulfils the
	// embedded roots/list request and retries the call.
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "roots"}); err != nil {
		log.Fatal(err)
	}
	// Output: [file://a file://b]
}

// !-roots

// !+rootslistchanged

func Example_rootsListChanged() {
	ctx := context.Background()

	changed := make(chan struct{}, 2)
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, &mcp.ServerOptions{
		RootsListChangedHandler: func(context.Context, *mcp.RootsListChangedRequest) {
			changed <- struct{}{}
		},
	})

	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, nil)
	c.AddRoots(&mcp.Root{URI: "file:///project"})

	t1, t2 := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, t1, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ss.Close()

	// ListRoots is a server-initiated request, so this session negotiates a
	// protocol version that still allows one.
	cs, err := c.Connect(ctx, t2, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	// Roots added after the client connects notify every connected server.
	c.AddRoots(&mcp.Root{URI: "file:///scratch"})
	<-changed

	// The notification says only that the list changed, so read it back.
	res, err := ss.ListRoots(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, root := range res.Roots {
		fmt.Println(root.URI)
	}

	c.RemoveRoots("file:///scratch")
	<-changed
	fmt.Println("roots changed again")

	// Output:
	// file:///project
	// file:///scratch
	// roots changed again
}

// !-rootslistchanged

// !+sampling

func Example_sampling() {
	ctx := context.Background()

	// Create a client with a sampling handler.
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, &mcp.ClientOptions{
		CreateMessageHandler: func(_ context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			return &mcp.CreateMessageResult{
				Content: &mcp.TextContent{
					Text: "would have created a message",
				},
			}, nil
		},
	})

	// Connect the server and client...
	ct, st := mcp.NewInMemoryTransports()
	// Create a server with a tool that requests sampling via the multi
	// round-trip pattern (SEP-2322): server-to-client requests are no longer
	// sent as standalone JSON-RPC calls on protocol version >= 2026-07-28.
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "sample"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if len(req.Params.InputResponses) == 0 {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{"msg": &mcp.CreateMessageParams{}},
			}, nil, nil
		}
		msg := req.Params.InputResponses["msg"].(*mcp.CreateMessageWithToolsResult)
		return &mcp.CallToolResult{Content: msg.Content}, nil, nil
	})
	session, err := s.Connect(ctx, st, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	clientSession, err := c.Connect(ctx, ct, nil)
	if err != nil {
		log.Fatal(err)
	}

	res, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "sample"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content[0].(*mcp.TextContent).Text)
	// Output: would have created a message
}

// !-sampling

// !+elicitation

func Example_elicitation() {
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()

	// Create a server with a tool that requests elicitation via the multi
	// round-trip pattern (SEP-2322): server-to-client requests are no longer
	// sent as standalone JSON-RPC calls on protocol version >= 2026-07-28.
	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "ask"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if len(req.Params.InputResponses) == 0 {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{"input": &mcp.ElicitParams{
					Message: "This should fail",
					RequestedSchema: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"test": {Type: "string"},
						},
					},
				}},
			}, nil, nil
		}
		res := req.Params.InputResponses["input"].(*mcp.ElicitResult)
		fmt.Println(res.Content["test"])
		return &mcp.CallToolResult{}, nil, nil
	})
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ss.Close()

	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"test": "value"}}, nil
		},
	})
	clientSession, err := c.Connect(ctx, ct, nil)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "ask"}); err != nil {
		log.Fatal(err)
	}
	// Output: value
}

// !-elicitation

// !+elicitationschema

func Example_elicitationSchema() {
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()

	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "export_report"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if len(req.Params.InputResponses) == 0 {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{"format": &mcp.ElicitParams{
					Message: "Export quarterly-sales as which format?",
					RequestedSchema: &jsonschema.Schema{
						Type: "object",
						Properties: map[string]*jsonschema.Schema{
							"format": {
								Type:    "string",
								Title:   "Format",
								Enum:    []any{"pdf", "csv"},
								Default: json.RawMessage(`"pdf"`),
								Extra:   map[string]any{"enumNames": []any{"PDF document", "CSV spreadsheet"}},
							},
						},
					},
				}},
			}, nil, nil
		}
		res := req.Params.InputResponses["format"].(*mcp.ElicitResult)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Exported as " + res.Content["format"].(string)}},
		}, nil, nil
	})
	if _, err := s.Connect(ctx, st, nil); err != nil {
		log.Fatal(err)
	}

	// The user accepts without filling anything in.
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}, nil
		},
	})
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "export_report"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(res.Content[0].(*mcp.TextContent).Text)
	// Output: Exported as pdf
}

// !-elicitationschema

// !+elicitationcomplete

func Example_elicitationComplete() {
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()

	s := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "v0.0.1"}, nil)
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ss.Close()

	done := make(chan struct{})
	c := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0.0.1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{
			Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}},
		},
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			fmt.Println("opening", req.Params.URL)
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
		ElicitationCompleteHandler: func(_ context.Context, req *mcp.ElicitationCompleteNotificationRequest) {
			fmt.Println("flow finished:", req.Params.ElicitationID)
			close(done)
		},
	})
	cs, err := c.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		log.Fatal(err)
	}
	defer cs.Close()

	const elicitationID = "connect-calendar-1"
	if _, err := ss.Elicit(ctx, &mcp.ElicitParams{
		Message:       "Grant calendar access",
		URL:           "https://calendar.example.com/consent?state=" + elicitationID,
		ElicitationID: elicitationID,
	}); err != nil {
		log.Fatal(err)
	}

	// The hosted page redirects back to the server, whose callback endpoint
	// signals that the user is done.
	if err := ss.NotifyElicitationComplete(ctx, &mcp.ElicitationCompleteParams{ElicitationID: elicitationID}); err != nil {
		log.Fatal(err)
	}
	<-done

	// Output:
	// opening https://calendar.example.com/consent?state=connect-calendar-1
	// flow finished: connect-calendar-1
}

// !-elicitationcomplete
