// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const (
	ProtocolVersionHeader        = "Mcp-Protocol-Version"
	SessionIDHeader              = "Mcp-Session-Id"
	LastEventIDHeader            = "Last-Event-ID"
	MethodHeader                 = "Mcp-Method"
	NameHeader                   = "Mcp-Name"
	MinVersionForStandardHeaders = "2026-06-XX"
)

func extractName(method string, params json.RawMessage) (string, bool) {
	switch method {
	case "tools/call":
		var p CallToolParams
		if err := json.Unmarshal(params, &p); err == nil {
			return p.Name, true
		}
	case "prompts/get":
		var p GetPromptParams
		if err := json.Unmarshal(params, &p); err == nil {
			return p.Name, true
		}
	case "resources/read":
		var p ReadResourceParams
		if err := json.Unmarshal(params, &p); err == nil {
			return p.URI, true
		}
	}

	return "", false
}

func setStandardHeaders(httpReq *http.Request, msg jsonrpc.Message) {
	if msg == nil {
		return
	}
	if httpReq.Header.Get(ProtocolVersionHeader) == "" || httpReq.Header.Get(ProtocolVersionHeader) < MinVersionForStandardHeaders {
		return
	}

	switch msg := msg.(type) {
	case *jsonrpc.Request:
		httpReq.Header.Set(MethodHeader, msg.Method)
		if name, ok := extractName(msg.Method, msg.Params); ok {
			httpReq.Header.Set(NameHeader, name)
		}
	}
}

func validateMcpHeaders(req *http.Request, msg jsonrpc.Message) error {
	protocolVersion := req.Header.Get(ProtocolVersionHeader)
	if protocolVersion == "" || protocolVersion < MinVersionForStandardHeaders {
		return nil
	}

	switch msg := msg.(type) {
	case *jsonrpc.Request:
		methodInHeader := req.Header.Get(MethodHeader)
		if methodInHeader == "" {
			return errors.New("missing required Mcp-Method header")
		}
		if methodInHeader != msg.Method {
			return fmt.Errorf("Header mismatch: Mcp-Method header value '%s' does not match body value '%s'", methodInHeader, msg.Method)
		}

		if msg.Method == "tools/call" || msg.Method == "resources/read" || msg.Method == "prompts/get" {
			nameInHeader := req.Header.Get(NameHeader)
			if nameInHeader == "" {
				return fmt.Errorf("missing required Mcp-Name header for method %q", msg.Method)
			}
			if nameInBody, ok := extractName(msg.Method, msg.Params); ok {
				if nameInHeader != nameInBody {
					return fmt.Errorf("Header mismatch: Mcp-Name header value '%s' does not match body value '%s'", nameInHeader, nameInBody)
				}
			}
		}
	}
	return nil
}
