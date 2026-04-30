// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	internaljson "github.com/modelcontextprotocol/go-sdk/internal/json"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const (
	protocolVersionHeader        = "Mcp-Protocol-Version"
	sessionIDHeader              = "Mcp-Session-Id"
	lastEventIDHeader            = "Last-Event-ID"
	methodHeader                 = "Mcp-Method"
	nameHeader                   = "Mcp-Name"
	paramHeaderPrefix            = "Mcp-Param-"
	minVersionForStandardHeaders = protocolVersion20260630
	mcpHeaderExtension           = "x-mcp-header"
)


func extractName(method string, params json.RawMessage) (string, bool) {
	switch method {
	case "tools/call":
		var p CallToolParams
		if err := internaljson.Unmarshal(params, &p); err == nil {
			return p.Name, true
		}
	case "prompts/get":
		var p GetPromptParams
		if err := internaljson.Unmarshal(params, &p); err == nil {
			return p.Name, true
		}
	case "resources/read":
		var p ReadResourceParams
		if err := internaljson.Unmarshal(params, &p); err == nil {
			return p.URI, true
		}
	}

	return "", false
}

func extractSchemaProperties(schema any) map[string]any {
	s, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := s["properties"].(map[string]any)
	if !ok {
		return nil
	}
	return props
}

// extractToolParamHeaders returns a map of parameter name to header name
// for all properties in the tool's InputSchema that have an x-mcp-header
// annotation.
func extractToolParamHeaders(tool *Tool) map[string]string {
	props := extractSchemaProperties(tool.InputSchema)
	if props == nil {
		return nil
	}
	result := make(map[string]string)
	for propName, propSchema := range props {
		ps, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		headerName, ok := ps[mcpHeaderExtension].(string)
		if !ok || headerName == "" {
			continue
		}
		result[propName] = headerName
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func primitiveToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// unmarshalPrimitive unmarshals a JSON value into a Go primitive
// (string, float64, or bool). Returns nil for non-primitive types.
func unmarshalPrimitive(raw json.RawMessage) any {
	var val any
	if err := internaljson.Unmarshal(raw, &val); err != nil {
		return nil
	}
	switch val.(type) {
	case string, float64, bool:
		return val
	default:
		return nil
	}
}

// setStandardHeaders populates standard MCP headers.
// It requires the protocol version header to be set.
func setStandardHeaders(header http.Header, msg jsonrpc.Message) {
	if msg == nil {
		return
	}
	if header.Get(protocolVersionHeader) == "" || header.Get(protocolVersionHeader) < minVersionForStandardHeaders {
		return
	}

	switch msg := msg.(type) {
	case *jsonrpc.Request:
		header.Set(methodHeader, msg.Method)
		if name, ok := extractName(msg.Method, msg.Params); ok {
			header.Set(nameHeader, name)
		}
		if msg.Method == "tools/call" {
			if tool, ok := msg.Extra.(*Tool); ok && tool != nil {
				setParamHeaders(header, tool, msg.Params)
			}
		}
	}
}

// setParamHeaders reads x-mcp-header annotations from the tool's InputSchema
// and sets Mcp-Param-{Name} headers on the HTTP request.
func setParamHeaders(header http.Header, tool *Tool, params json.RawMessage) {
	paramHeaders := extractToolParamHeaders(tool)
	if len(paramHeaders) == 0 {
		return
	}

	var raw struct {
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := internaljson.Unmarshal(params, &raw); err != nil || raw.Arguments == nil {
		return
	}

	for paramName, headerName := range paramHeaders {
		argRaw, ok := raw.Arguments[paramName]
		if !ok {
			continue
		}
		if string(argRaw) == "null" {
			continue
		}
		val := unmarshalPrimitive(argRaw)
		if val == nil {
			continue
		}
		encoded, ok := encodeHeaderValue(val)
		if !ok {
			continue
		}
		header.Set(paramHeaderPrefix+headerName, encoded)
	}
}

// filterValidTools returns only tools that have valid
// x-mcp-header annotations. Invalid tools are logged and excluded.
func filterValidTools(tools []*Tool) []*Tool {
	result := make([]*Tool, 0, len(tools))
	for _, tool := range tools {
		if err := validateToolParamHeaders(tool); err != nil {
			log.Printf("mcp: excluding tool %q from tools/list: %v", tool.Name, err)
			continue
		}
		result = append(result, tool)
	}
	return result
}

// validateToolParamHeaders checks that a tool's x-mcp-header annotations
// are valid.
func validateToolParamHeaders(tool *Tool) error {
	props := extractSchemaProperties(tool.InputSchema)
	if props == nil {
		return nil
	}

	seen := make(map[string]bool)
	for propName, propSchema := range props {
		ps, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		headerNameRaw, exists := ps[mcpHeaderExtension]
		if !exists {
			continue
		}
		headerName, ok := headerNameRaw.(string)
		if !ok || headerName == "" {
			return fmt.Errorf("property %q: x-mcp-header must be a non-empty string", propName)
		}
		if err := validateHeaderName(headerName); err != nil {
			return fmt.Errorf("property %q: %w", propName, err)
		}
		lower := strings.ToLower(headerName)
		if seen[lower] {
			return fmt.Errorf("property %q: duplicate x-mcp-header value %q (case-insensitive)", propName, headerName)
		}
		seen[lower] = true

		propType, _ := ps["type"].(string)
		if propType != "" && propType != "string" && propType != "number" && propType != "integer" && propType != "boolean" {
			return fmt.Errorf("property %q: x-mcp-header can only be applied to primitive types, got %q", propName, propType)
		}
	}

	for propName, propSchema := range props {
		ps, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		if err := checkForNestedHeaders(ps, propName); err != nil {
			return err
		}
	}
	return nil
}

func checkForNestedHeaders(schema map[string]any, path string) error {
	nestedProps := extractSchemaProperties(schema)
	if nestedProps == nil {
		return nil
	}
	for propName, propSchema := range nestedProps {
		ps, ok := propSchema.(map[string]any)
		if !ok {
			continue
		}
		if _, exists := ps[mcpHeaderExtension]; exists {
			return fmt.Errorf("property %q: x-mcp-header cannot be applied to nested properties", path+"."+propName)
		}
		if err := checkForNestedHeaders(ps, path+"."+propName); err != nil {
			return err
		}
	}
	return nil
}

// validateHeaderName checks that a header name contains only valid
// ASCII characters (excluding space and ':').
func validateHeaderName(name string) error {
	for _, c := range name {
		if c <= 0x20 || c > 0x7E || c == ':' {
			return fmt.Errorf("x-mcp-header value %q contains invalid character %q", name, c)
		}
	}
	return nil
}

func validateMcpHeaders(header http.Header, msg jsonrpc.Message, tool *Tool) error {
	protocolVersion := header.Get(protocolVersionHeader)
	if protocolVersion == "" || protocolVersion < minVersionForStandardHeaders {
		return nil
	}

	switch msg := msg.(type) {
	case *jsonrpc.Request:
		methodInHeader := header.Get(methodHeader)
		if methodInHeader == "" {
			return errors.New("missing required Mcp-Method header")
		}
		if methodInHeader != msg.Method {
			return fmt.Errorf("header mismatch: Mcp-Method header value '%s' does not match body value '%s'", methodInHeader, msg.Method)
		}

		if msg.Method == "tools/call" || msg.Method == "resources/read" || msg.Method == "prompts/get" {
			nameInHeader := header.Get(nameHeader)
			if nameInHeader == "" {
				return fmt.Errorf("missing required Mcp-Name header for method %q", msg.Method)
			}
			nameInBody, ok := extractName(msg.Method, msg.Params)
			if !ok {
				return fmt.Errorf("failed to extract name from parameters for method %q", msg.Method)
			}
			if nameInHeader != nameInBody {
				return fmt.Errorf("header mismatch: Mcp-Name header value '%s' does not match body value '%s'", nameInHeader, nameInBody)
			}
		}

		if msg.Method == "tools/call" && tool != nil {
			if err := validateParamHeaders(header, msg, tool); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateParamHeaders(header http.Header, msg *jsonrpc.Request, tool *Tool) error {
	paramHeaders := extractToolParamHeaders(tool)
	if len(paramHeaders) == 0 {
		return nil
	}

	var raw struct {
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := internaljson.Unmarshal(msg.Params, &raw); err != nil {
		return nil
	}

	for paramName, headerName := range paramHeaders {
		fullHeader := paramHeaderPrefix + headerName
		headerVal := header.Get(fullHeader)
		argRaw, argExists := raw.Arguments[paramName]

		if !argExists || string(argRaw) == "null" {
			if headerVal != "" {
				return fmt.Errorf("header mismatch: unexpected %s header for absent or null parameter %q", fullHeader, paramName)
			}
			continue
		}

		if headerVal == "" {
			return fmt.Errorf("header mismatch: missing %s header for parameter %q", fullHeader, paramName)
		}

		decoded, ok := decodeHeaderValue(headerVal)
		if !ok {
			return fmt.Errorf("header mismatch: %s header contains invalid Base64 encoding", fullHeader)
		}

		bodyVal := unmarshalPrimitive(argRaw)
		if bodyVal == nil {
			continue
		}
		expected := primitiveToString(bodyVal)

		if decoded != expected {
			return fmt.Errorf("header mismatch: %s header value '%s' does not match body value", fullHeader, headerVal)
		}
	}
	return nil
}
