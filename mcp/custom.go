// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"reflect"
)

// ParamsBase can be embedded in custom parameter structs to satisfy the
// [Params] interface. It provides the required [Meta] field and the unexported
// isParams marker method.
//
//	type SearchParams struct {
//	    mcp.ParamsBase
//	    Query string `json:"query"`
//	}
type ParamsBase struct {
	Meta `json:"_meta,omitempty"`
}

func (*ParamsBase) isParams() {}

// ResultBase can be embedded in custom result structs to satisfy the
// [Result] interface. It provides the required [Meta] field and the unexported
// isResult marker method.
//
//	type SearchResult struct {
//	    mcp.ResultBase
//	    Hits []string `json:"hits"`
//	}
type ResultBase struct {
	Meta `json:"_meta,omitempty"`
}

func (*ResultBase) isResult() {}

// AddReceivingCustomMethod registers a handler for a custom (non-standard)
// JSON-RPC method on the server.
//
// When a client sends a request with the given method name, the params will be
// unmarshaled into P, the handler will be called, and the returned R will be
// marshaled as the JSON-RPC result.
//
// Custom methods go through the server's middleware chain just like standard
// MCP methods (tools/call, prompts/list, etc.).
//
// P and R must implement [Params] and [Result] respectively, which is most
// easily done by embedding [ParamsBase] and [ResultBase]:
//
//	type SearchParams struct {
//	    mcp.ParamsBase
//	    Query string `json:"query"`
//	}
//
//	type SearchResult struct {
//	    mcp.ResultBase
//	    Hits []string `json:"hits"`
//	}
//
//	mcp.AddReceivingCustomMethod(server, "acme/search",
//	    func(ctx context.Context, ss *mcp.ServerSession, params *SearchParams) (*SearchResult, error) {
//	        return &SearchResult{Hits: []string{"result"}}, nil
//	    })
func AddReceivingCustomMethod[P paramsPtr[T], R Result, T any](
	s *Server,
	method string,
	handler func(ctx context.Context, ss *ServerSession, params P) (R, error),
) {
	typed := typedServerMethodHandler[P, R](func(ctx context.Context, req *ServerRequest[P]) (R, error) {
		return handler(ctx, req.Session, req.Params)
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	s.customMethods[method] = newServerMethodInfo(typed, missingParamsOK)
}

// AddSendingCustomMethod registers a custom method that the client can send
// to the server and returns a typed caller function.
//
// The returned function calls the custom method through the client's sending
// middleware chain, with full type safety on both params and result.
//
//	callSearch := mcp.AddSendingCustomMethod[*SearchParams, *SearchResult](c, "acme/search")
//	result, err := callSearch(ctx, cs, &SearchParams{Query: "hello"})
func AddSendingCustomMethod[P paramsPtr[PT], R Result, PT any](
	c *Client,
	method string,
) func(ctx context.Context, cs *ClientSession, params P) (R, error) {
	mi := methodInfo{
		newResult: func() Result {
			return reflect.New(reflect.TypeFor[R]().Elem()).Interface().(R)
		},
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.customSendMethods[method] = mi

	return func(ctx context.Context, cs *ClientSession, params P) (R, error) {
		return handleSend[R](ctx, method, &ClientRequest[P]{
			Session: cs,
			Params:  params,
		})
	}
}
