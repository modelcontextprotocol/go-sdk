// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// GlobalRateLimiterMiddleware creates a middleware that applies a global rate limit.
// Every request attempting to pass through will try to acquire a token.
// If a token cannot be acquired immediately, the request will be rejected.
func GlobalRateLimiterMiddleware(limiter *rate.Limiter) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if !limiter.Allow() {
				return nil, errors.New("JSON RPC overloaded")
			}
			return next(ctx, method, req)
		}
	}
}

// PerMethodRateLimiterMiddleware creates a middleware that applies rate limiting
// on a per-method basis.
// Methods not specified in limiters will not be rate limited by this middleware.
func PerMethodRateLimiterMiddleware(limiters map[string]*rate.Limiter) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if limiter, ok := limiters[method]; ok {
				if !limiter.Allow() {
					return nil, errors.New("JSON RPC overloaded")
				}
			}
			return next(ctx, method, req)
		}
	}
}

// PerSessionRateLimiterMiddleware creates a middleware that applies rate limiting
// on a per-session basis for receiving requests.
func PerSessionRateLimiterMiddleware(limit rate.Limit, burst int) mcp.Middleware {
	// A map to store limiters, keyed by the session ID.
	var (
		sessionLimiters = make(map[string]*rate.Limiter)
		mu              sync.Mutex
	)

	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// It's possible that session.ID() may be empty at this point in time
			// for some transports (e.g., stdio) or until the MCP initialize handshake
			// has completed.
			sessionID := req.GetSession().ID()
			if sessionID == "" {
				// In this situation, you could apply a single global identifier
				// if session ID is empty or bypass the rate limiter.
				// In this example, we bypass the rate limiter.
				log.Printf("Warning: Session ID is empty for method %q. Skipping per-session rate limiting.", method)
				return next(ctx, method, req) // Skip limiting if ID is unavailable
			}
			mu.Lock()
			limiter, ok := sessionLimiters[sessionID]
			if !ok {
				limiter = rate.NewLimiter(limit, burst)
				sessionLimiters[sessionID] = limiter
			}
			mu.Unlock()
			if !limiter.Allow() {
				return nil, errors.New("JSON RPC overloaded")
			}
			return next(ctx, method, req)
		}
	}
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "greeter1", Version: "v0.0.1"}, nil)
	server.AddReceivingMiddleware(GlobalRateLimiterMiddleware(rate.NewLimiter(rate.Every(time.Second/5), 10)))
	server.AddReceivingMiddleware(PerMethodRateLimiterMiddleware(map[string]*rate.Limiter{
		"callTool":  rate.NewLimiter(rate.Every(time.Second), 5),  // once a second with a burst up to 5
		"listTools": rate.NewLimiter(rate.Every(time.Minute), 20), // once a minute with a burst up to 20
	}))
	server.AddReceivingMiddleware(PerSessionRateLimiterMiddleware(rate.Every(time.Second/5), 10))
	// Run Server logic.
	log.Println("MCP Server instance created with Middleware (but not running).")
	log.Println("This example demonstrates configuration, not live interaction.")
}
