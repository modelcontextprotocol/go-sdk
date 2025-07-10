// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var nextProgressToken atomic.Int64

// This middleware function adds a progress token to every outgoing request
// from the client.
func Example_progressMiddleware() {
	c := mcp.NewClient(testImpl, nil)
	c.AddSendingMiddleware(addProgressToken[*mcp.ClientSession])
	_ = c
}

func addProgressToken[S mcp.Session](h mcp.MethodHandler[S]) mcp.MethodHandler[S] {
	return func(ctx context.Context, s S, method string, params mcp.Params) (result mcp.Result, err error) {
		if rp, ok := params.(mcp.RequestParams); ok {
			rp.SetProgressToken(nextProgressToken.Add(1))
		}
		return h(ctx, s, method, params)
	}
}
