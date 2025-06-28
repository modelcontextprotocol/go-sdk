// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// GlobalRateLimiterMiddleware creates a middleware that applies a global rate limit.
// Every request attempting to pass through will try to acquire a token.
// If a token cannot be acquired immediately, the request will be rejected.
func GlobalRateLimiterMiddleware[S mcp.Session](limiter *rate.Limiter) mcp.Middleware[S] {
	return func(next mcp.MethodHandler[S]) mcp.MethodHandler[S] {
		return func(ctx context.Context, session S, method string, params mcp.Params) (mcp.Result, error) {
			if !limiter.Allow() {
				return nil, errors.New("JSON RPC overloaded")
			}
			return next(ctx, session, method, params)
		}
	}
}

// PerMethodRateLimiterMiddleware creates a middleware that applies rate limiting
// on a per-method basis.
// Methods not specified in limiters will not be rate limited by this middleware.
func PerMethodRateLimiterMiddleware[S mcp.Session](limiters map[string]*rate.Limiter) mcp.Middleware[S] {
	return func(next mcp.MethodHandler[S]) mcp.MethodHandler[S] {
		return func(ctx context.Context, session S, method string, params mcp.Params) (mcp.Result, error) {
			if limiter, ok := limiters[method]; ok {
				if !limiter.Allow() {
					return nil, errors.New("JSON RPC overloaded")
				}
			}
			return next(ctx, session, method, params)
		}
	}
}

func main() {
	server := mcp.NewServer("greeter1", "v0.0.1", nil)
	server.AddReceivingMiddleware(GlobalRateLimiterMiddleware[*mcp.ServerSession](rate.NewLimiter(rate.Every(time.Second/5), 10)))
	server.AddReceivingMiddleware(PerMethodRateLimiterMiddleware[*mcp.ServerSession](map[string]*rate.Limiter{
		"callTool":  rate.NewLimiter(rate.Every(time.Second), 5),  // once a second with a burst up to 5
		"listTools": rate.NewLimiter(rate.Every(time.Minute), 20), // once a minute with a burst up to 20
	}))
	// Run Server logic.
}
