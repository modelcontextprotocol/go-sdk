// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	base64Prefix = "=?base64?"
	base64Suffix = "?="
)

// encodeHeaderValue converts a parameter value to an HTTP header-safe string
// per the SEP-2243 encoding rules:
//   - string: used as-is if safe ASCII, otherwise Base64 encoded
//   - number (float64): decimal string representation
//   - bool: lowercase "true" or "false"
//   - nil: returns "", false
//
// Values that contain non-ASCII characters, control characters, or
// leading/trailing whitespace are Base64-encoded with the =?base64?...?= wrapper.
func encodeHeaderValue(value any) (string, bool) {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case float64:
		s = fmt.Sprintf("%g", v)
	case bool:
		if v {
			s = "true"
		} else {
			s = "false"
		}
	default:
		return "", false
	}

	if requiresBase64Encoding(s) {
		return encodeBase64(s), true
	}
	return s, true
}

// decodeHeaderValue decodes a header value that may be Base64-encoded
// with the =?base64?...?= wrapper.
func decodeHeaderValue(headerValue string) (string, bool) {
	if len(headerValue) == 0 {
		return headerValue, true
	}

	if strings.HasPrefix(strings.ToLower(headerValue), strings.ToLower(base64Prefix)) &&
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
