// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	base64Prefix                 = "=?base64?"
	base64Suffix                 = "?="
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

// headerSchemaProperty captures the fields needed for x-mcp-header processing.
type headerSchemaProperty struct {
	Type       string                          `json:"type"`
	XMCPHeader json.RawMessage                 `json:"x-mcp-header,omitempty"`
	Properties map[string]headerSchemaProperty `json:"properties,omitempty"`
}

// unmarshalSchemaProperties normalizes any InputSchema type
// (*jsonschema.Schema, map[string]any, or json.RawMessage) into a common
// representation by marshaling to JSON and unmarshaling only the fields we need.
func unmarshalSchemaProperties(schema any) map[string]headerSchemaProperty {
	var s headerSchemaProperty
	if err := remarshal(schema, &s); err != nil {
		return nil
	}
	return s.Properties
}

// extractParamHeaderAnnotations returns a map of parameter name to header name
// for all properties in the tool's InputSchema that have an x-mcp-header
// annotation.
func extractParamHeaderAnnotations(tool *Tool) map[string]string {
	props := unmarshalSchemaProperties(tool.InputSchema)
	if len(props) == 0 {
		return nil
	}
	result := make(map[string]string)
	for propName, prop := range props {
		var headerName string
		if err := json.Unmarshal(prop.XMCPHeader, &headerName); err != nil || headerName == "" {
			continue
		}
		result[propName] = headerName
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// primitiveToString conversion.
// Returns false in the second return value if the argument is not a primitive value.
func primitiveToString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case float64:
		return fmt.Sprintf("%g", v), true
	case bool:
		return fmt.Sprintf("%t", v), true
	default:
		return "", false
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
func setStandardHeaders(ctx context.Context, header http.Header, msg jsonrpc.Message) {
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
			if tool, ok := ctx.Value(toolContextKey).(*Tool); ok && tool != nil {
				for k, v := range generateParamHeaders(tool, msg.Params) {
					header.Set(k, v)
				}
			}
		}
	}
}

// generateParamHeaders reads x-mcp-header annotations from the tool's InputSchema
// and returns the Mcp-Param-{Name} headers to be set on the HTTP request.
func generateParamHeaders(tool *Tool, params json.RawMessage) map[string]string {
	paramHeaders := extractParamHeaderAnnotations(tool)
	if len(paramHeaders) == 0 {
		return nil
	}

	var raw struct {
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := internaljson.Unmarshal(params, &raw); err != nil || raw.Arguments == nil {
		return nil
	}

	res := make(map[string]string)
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
		res[paramHeaderPrefix+headerName] = encoded
	}
	return res
}

// filterValidTools returns only tools that have valid
// x-mcp-header annotations. Invalid tools are logged and excluded.
func filterValidTools(logger *slog.Logger, tools []*Tool) []*Tool {
	logger = ensureLogger(logger)
	result := make([]*Tool, 0, len(tools))
	for _, tool := range tools {
		if err := validateParamHeaderAnnotations(tool); err != nil {
			logger.Error("excluding tool from tools/list", "tool", tool.Name, "error", err)
			continue
		}
		result = append(result, tool)
	}
	return result
}

// validateParamHeaderAnnotations checks that a tool's x-mcp-header annotations
// are valid.
func validateParamHeaderAnnotations(tool *Tool) error {
	props := unmarshalSchemaProperties(tool.InputSchema)
	if len(props) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	for propName, prop := range props {
		if err := checkForNestedHeaders(prop, propName); err != nil {
			return err
		}
		if prop.XMCPHeader == nil {
			continue
		}
		var headerName string
		if err := json.Unmarshal(prop.XMCPHeader, &headerName); err != nil || headerName == "" {
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

		if prop.Type != "string" && prop.Type != "number" && prop.Type != "integer" && prop.Type != "boolean" {
			return fmt.Errorf("property %q: x-mcp-header can only be applied to primitive types, got %v", propName, prop.Type)
		}
	}
	return nil
}

func checkForNestedHeaders(prop headerSchemaProperty, path string) error {
	for propName, nested := range prop.Properties {
		if nested.XMCPHeader != nil {
			return fmt.Errorf("property %q: x-mcp-header cannot be applied to nested properties", path+"."+propName)
		}
		if err := checkForNestedHeaders(nested, path+"."+propName); err != nil {
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

func validateMcpHeaders(header http.Header, msg jsonrpc.Message, toolLookup func(string) (*serverTool, bool)) error {
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

		var nameInBody string
		if msg.Method == "tools/call" || msg.Method == "resources/read" || msg.Method == "prompts/get" {
			nameInHeader := header.Get(nameHeader)
			if nameInHeader == "" {
				return fmt.Errorf("missing required Mcp-Name header for method %q", msg.Method)
			}
			var ok bool
			nameInBody, ok = extractName(msg.Method, msg.Params)
			if !ok {
				return fmt.Errorf("failed to extract name from parameters for method %q", msg.Method)
			}
			if nameInHeader != nameInBody {
				return fmt.Errorf("header mismatch: Mcp-Name header value '%s' does not match body value '%s'", nameInHeader, nameInBody)
			}
		}

		if msg.Method == "tools/call" && toolLookup != nil {
			if st, ok := toolLookup(nameInBody); ok && st != nil {
				if err := validateParamHeaders(header, msg, st.tool); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateParamHeaders(header http.Header, msg *jsonrpc.Request, tool *Tool) error {
	paramHeaders := extractParamHeaderAnnotations(tool)
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
			return fmt.Errorf("header mismatch: %s header present but body parameter %q is not a primitive type", fullHeader, paramName)
		}
		expected, ok := primitiveToString(bodyVal)
		if !ok {
			return fmt.Errorf("header mismatch: %s header present but body parameter %q is not a primitive type", fullHeader, paramName)
		}

		// TODO: String comparison may not work ideally for numbers
		if decoded != expected {
			return fmt.Errorf("header mismatch: %s header value '%s' does not match body value", fullHeader, headerVal)
		}
	}
	return nil
}

// encodeHeaderValue converts a parameter value to an HTTP header-safe string
// per the SEP-2243 encoding rules:
//   - string: used as-is if safe ASCII, otherwise Base64 encoded
//   - number (float64): decimal string representation
//   - bool: lowercase "true" or "false"
//
// Values that contain non-ASCII characters, control characters, or
// leading/trailing whitespace are Base64-encoded with the =?base64?...?= wrapper.
//
// The second return value is false if the value is not a supported primitive type.
func encodeHeaderValue(value any) (string, bool) {
	s, ok := primitiveToString(value)
	if !ok {
		return "", false
	}
	if requiresBase64Encoding(s) {
		return encodeBase64(s), true
	}
	return s, true
}

// decodeHeaderValue decodes a header value that may be Base64-encoded
// with the =?base64?...?= wrapper.
//
// The second return value is false if the header value is not a valid Base64 encoded value.
func decodeHeaderValue(headerValue string) (string, bool) {
	if len(headerValue) == 0 {
		return headerValue, true
	}

	if strings.HasPrefix(strings.ToLower(headerValue), base64Prefix) &&
		strings.HasSuffix(headerValue, base64Suffix) {
		encoded := headerValue[len(base64Prefix) : len(headerValue)-len(base64Suffix)]
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", false
		}
		return string(decoded), true
	}
	return headerValue, true
}

func requiresBase64Encoding(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return true
	}
	for _, c := range s {
		if c < 0x20 || c > 0x7E {
			return true
		}
	}
	return false
}

func encodeBase64(s string) string {
	return base64Prefix + base64.StdEncoding.EncodeToString([]byte(s)) + base64Suffix
}
