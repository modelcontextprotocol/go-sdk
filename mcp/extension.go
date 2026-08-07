// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"sync"
)

// CustomMethod captures the method name and parameter/result types for a
// custom JSON-RPC method. Extension authors define a package-level var using
// [NewCustomMethod]; consumers call the resulting methods without ever writing
// generic type parameters or method-name strings.
//
// For the client-to-server direction:
//
//	var Method = mcp.NewCustomMethod[*MyParams, *MyResult]("acme/method")
//
//	// Extension author wires server and client once:
//	Method.RegisterServerReceiving(server, MyHandler)
//	Method.RegisterClientSending(client)
//
//	// Consumer calls with no generics visible:
//	result, err := Method.Call(ctx, cs, &MyParams{...})
//
// For the server-to-client direction use [CustomMethod.RegisterServerSending],
// [CustomMethod.RegisterClientReceiving], and [CustomMethod.ServerCall].
//
// P, R, and T are phantom type parameters — they are not stored in the struct
// but thread through to the wrapped generic functions so call sites stay
// type-safe without repeating type arguments.
type CustomMethod[P paramsPtr[T], R Result, T any] struct {
	name string
}

// NewCustomMethod creates a [CustomMethod] that captures the method name and
// its parameter and result types. The name must not be the name of a standard
// MCP method.
func NewCustomMethod[P paramsPtr[T], R Result, T any](name string) *CustomMethod[P, R, T] {
	return &CustomMethod[P, R, T]{name: name}
}

// Name returns the JSON-RPC method name.
func (m *CustomMethod[P, R, T]) Name() string { return m.name }

// RegisterServerReceiving registers handler on s to handle incoming requests
// for this method from clients. It wraps [AddReceivingCustomMethod].
func (m *CustomMethod[P, R, T]) RegisterServerReceiving(s *Server, handler func(ctx context.Context, ss *ServerSession, params P) (R, error)) error {
	return AddReceivingCustomMethod(s, m.name, handler)
}

// RegisterServerSending registers this method on s so that the server may
// call clients via [CustomMethod.ServerCall]. It wraps
// [AddServerSendingCustomMethod].
func (m *CustomMethod[P, R, T]) RegisterServerSending(s *Server) error {
	return AddServerSendingCustomMethod[P, R](s, m.name)
}

// RegisterClientSending registers this method on c so that the client may
// send it to a server via [CustomMethod.Call]. It wraps [AddSendingCustomMethod].
func (m *CustomMethod[P, R, T]) RegisterClientSending(c *Client) error {
	return AddSendingCustomMethod[P, R](c, m.name)
}

// RegisterClientReceiving registers handler on c to handle incoming requests
// for this method from servers. It wraps [AddClientReceivingCustomMethod].
func (m *CustomMethod[P, R, T]) RegisterClientReceiving(c *Client, handler func(ctx context.Context, cs *ClientSession, params P) (R, error)) error {
	return AddClientReceivingCustomMethod(c, m.name, handler)
}

// Call invokes this method on the server via cs. It wraps [CallCustomMethod].
// The method must have been registered on the client via [CustomMethod.RegisterClientSending].
func (m *CustomMethod[P, R, T]) Call(ctx context.Context, cs *ClientSession, params P) (R, error) {
	return CallCustomMethod[P, R](ctx, cs, m.name, params)
}

// ServerCall invokes this method on the client via ss. It wraps
// [ServerCallCustomMethod]. The method must have been registered on the server
// via [CustomMethod.RegisterServerSending].
func (m *CustomMethod[P, R, T]) ServerCall(ctx context.Context, ss *ServerSession, params P) (R, error) {
	return ServerCallCustomMethod[P, R](ctx, ss, m.name, params)
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
func RegisterExtension(ext Extension) {
	extensionsMu.Lock()
	defer extensionsMu.Unlock()
	extensions = append(extensions, ext)
}

func applyExtensionsToServer(s *Server) {
	applyExtensions(func(e Extension) func(*Server) error { return e.Server }, s)
}

func applyExtensionsToClient(c *Client) {
	applyExtensions(func(e Extension) func(*Client) error { return e.Client }, c)
}

func applyExtensions[T any](get func(Extension) func(T) error, arg T) {
	extensionsMu.Lock()
	exts := append([]Extension(nil), extensions...)
	extensionsMu.Unlock()
	runExtensions(exts, get, arg)
}

func runExtensions[T any](exts []Extension, get func(Extension) func(T) error, arg T) {
	for _, ext := range exts {
		if fn := get(ext); fn != nil {
			if err := fn(arg); err != nil {
				panic(fmt.Errorf("mcp: applying extension: %w", err))
			}
		}
	}
}
