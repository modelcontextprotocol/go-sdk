// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package jsonrpc exposes part of a JSON-RPC v2 implementation
// for use by mcp transport authors.
package jsonrpc

import "github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"

type (
	// ID is a JSON-RPC request ID.
	ID = jsonrpc2.ID
	// Message is a JSON-RPC message.
	Message = jsonrpc2.Message
	// Request is a JSON-RPC request.
	Request = jsonrpc2.Request
	// Response is a JSON-RPC response.
	Response = jsonrpc2.Response
)
