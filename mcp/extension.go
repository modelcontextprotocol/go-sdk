// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"sync"
)

// CustomMethod describes a custom client-to-server JSON-RPC method. Define it
// once with [NewCustomMethod] to reuse its name and types for registration and calls:
//
//	var Method = mcp.NewCustomMethod[*MyParams, *MyResult]("acme/method")
//
//	err := Method.RegisterServer(server, MyHandler)
//	err = Method.RegisterClient(client)
//	result, err := Method.Call(ctx, cs, &MyParams{...})
type CustomMethod[P paramsPtr[T], R Result, T any] struct {
	name string
}

// NewCustomMethod creates a [CustomMethod] that captures the method name and
// its parameter and result types. Registration rejects standard MCP method names.
func NewCustomMethod[P paramsPtr[T], R Result, T any](name string) *CustomMethod[P, R, T] {
	return &CustomMethod[P, R, T]{name: name}
}

// Name returns the JSON-RPC method name.
func (m *CustomMethod[P, R, T]) Name() string { return m.name }

// RegisterServer registers handler on s using [AddReceivingCustomMethod].
// Registering the same custom method again replaces its handler.
func (m *CustomMethod[P, R, T]) RegisterServer(s *Server, handler func(ctx context.Context, ss *ServerSession, params P) (R, error)) error {
	return AddReceivingCustomMethod(s, m.name, handler)
}

// RegisterClient registers this method on c using [AddSendingCustomMethod],
// so its sessions can invoke [CustomMethod.Call].
func (m *CustomMethod[P, R, T]) RegisterClient(c *Client) error {
	return AddSendingCustomMethod[P, R](c, m.name)
}

// Call invokes this method on the server via cs. It wraps [CallCustomMethod].
// The method must have been registered on the client via [CustomMethod.RegisterClient].
func (m *CustomMethod[P, R, T]) Call(ctx context.Context, cs *ClientSession, params P) (R, error) {
	return CallCustomMethod[P, R](ctx, cs, m.name, params)
}

// Extension describes a set of custom methods that can be auto-applied to
// every new [Server] and [Client] via [RegisterExtension].
//
// Extension authors typically call [RegisterExtension] in an init function so
// that importing the extension package is sufficient to wire everything up.
// Either field may be nil if the extension only applies to one side.
//
// If applying an extension returns an error (e.g. the method name shadows a
// standard method), [NewServer] or [NewClient] will panic.
type Extension struct {
	// Server, if non-nil, is called by [NewServer] to register the extension.
	Server func(*Server) error
	// Client, if non-nil, is called by [NewClient] to register the extension.
	Client func(*Client) error
}

var (
	extensionsMu sync.Mutex
	extensions   []Extension
)

// RegisterExtension adds ext to the global extension registry. [NewServer]
// and [NewClient] apply all registered extensions in registration order,
// before any per-instance extensions set in [ServerOptions.Extensions] or
// [ClientOptions.Extensions].
//
// RegisterExtension is safe for concurrent use and is typically called from
// init functions. For scoped registration that does not affect the whole
// process, use [ServerOptions.Extensions] / [ClientOptions.Extensions] instead.
// Registration does not affect existing clients or servers.
func RegisterExtension(ext Extension) {
	extensionsMu.Lock()
	defer extensionsMu.Unlock()
	extensions = append(extensions, ext)
}

func applyExtensions[T any](local []Extension, get func(Extension) func(T) error, arg T) {
	extensionsMu.Lock()
	exts := append([]Extension(nil), extensions...)
	extensionsMu.Unlock()
	// Apply callbacks outside the lock so they may register other extensions.
	for _, ext := range append(exts, local...) {
		if fn := get(ext); fn != nil {
			if err := fn(arg); err != nil {
				panic(fmt.Errorf("mcp: applying extension: %w", err))
			}
		}
	}
}
