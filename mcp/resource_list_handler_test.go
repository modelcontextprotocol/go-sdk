// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestListResourcesHandlerDynamicOnly(t *testing.T) {
	ctx := context.Background()
	var gotCursor string
	server := NewServer(testImpl, &ServerOptions{
		ListResourcesHandler: func(_ context.Context, req *ListResourcesRequest) (*ListResourcesResult, error) {
			gotCursor = req.Params.Cursor
			if req.Params.Cursor != "" {
				return &ListResourcesResult{Resources: []*Resource{}}, nil
			}
			return &ListResourcesResult{
				Resources:  []*Resource{{URI: "dynamic://a"}},
				NextCursor: "page2",
			}, nil
		},
	})
	client := NewClient(testImpl, nil)
	st, ct := NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []*Resource{{URI: "dynamic://a"}}
	if diff := cmp.Diff(want, res.Resources); diff != "" {
		t.Fatalf("first page mismatch (-want +got):\n%s", diff)
	}
	if gotCursor != "" {
		t.Fatalf("first page cursor = %q, want empty", gotCursor)
	}
	if res.NextCursor != "page2" {
		t.Fatalf("NextCursor = %q, want page2", res.NextCursor)
	}

	res2, err := cs.ListResources(ctx, &ListResourcesParams{Cursor: "page2"})
	if err != nil {
		t.Fatal(err)
	}
	if gotCursor != "page2" {
		t.Fatalf("second page cursor = %q, want page2", gotCursor)
	}
	if len(res2.Resources) != 0 {
		t.Fatalf("second page resources = %v, want empty", res2.Resources)
	}
}

func TestListResourcesHandlerComposeWithStatic(t *testing.T) {
	ctx := context.Background()
	handlerCalls := 0
	server := NewServer(testImpl, &ServerOptions{
		PageSize: 1,
		ListResourcesHandler: func(_ context.Context, req *ListResourcesRequest) (*ListResourcesResult, error) {
			handlerCalls++
			if req.Params.Cursor != "" {
				t.Fatalf("handler cursor = %q, want empty on first handler page", req.Params.Cursor)
			}
			return &ListResourcesResult{
				Resources: []*Resource{{URI: "dynamic://x"}},
			}, nil
		},
	})
	server.AddResource(&Resource{URI: "static://a"}, nil)
	server.AddResource(&Resource{URI: "static://b"}, nil)

	client := NewClient(testImpl, nil)
	st, ct := NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	page1, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Resources) != 1 || page1.Resources[0].URI != "static://a" {
		t.Fatalf("page1 = %v, want static://a", page1.Resources)
	}
	if page1.NextCursor == "" {
		t.Fatal("page1 NextCursor empty, want more pages")
	}

	page2, err := cs.ListResources(ctx, &ListResourcesParams{Cursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Resources) != 1 || page2.Resources[0].URI != "static://b" {
		t.Fatalf("page2 = %v, want static://b", page2.Resources)
	}
	if page2.NextCursor == "" {
		t.Fatal("page2 NextCursor empty, want handler phase")
	}

	page3, err := cs.ListResources(ctx, &ListResourcesParams{Cursor: page2.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page3.Resources) != 1 || page3.Resources[0].URI != "dynamic://x" {
		t.Fatalf("page3 = %v, want dynamic://x", page3.Resources)
	}
	if handlerCalls != 1 {
		t.Fatalf("handler calls = %d, want 1", handlerCalls)
	}
}

func TestListResourcesHandlerCapability(t *testing.T) {
	server := NewServer(testImpl, &ServerOptions{
		ListResourcesHandler: func(context.Context, *ListResourcesRequest) (*ListResourcesResult, error) {
			return &ListResourcesResult{Resources: []*Resource{}}, nil
		},
	})
	caps := server.capabilities()
	if caps.Resources == nil {
		t.Fatal("expected resources capability when ListResourcesHandler is set")
	}
}
