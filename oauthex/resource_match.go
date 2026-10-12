// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"net/url"
	"strings"
)

// resourceMatches reports whether actual is an acceptable resource identifier
// for the protected resource identified by expected.
//
// RFC 9728 section 3.3 requires protected resource metadata to identify the
// protected resource the client is asking about. In practice authorization
// servers often publish an origin or a parent path of the endpoint (for
// example "https://mcp.example.com/" for "https://mcp.example.com/mcp").
// The MCP TypeScript and Rust SDKs accept those identifiers, so this client
// does too. actual matches expected when:
//
//   - they are equal; or
//   - they share a scheme, host and port, and actual's path is the origin or
//     a segment-boundary prefix of expected's path; the query component is
//     ignored and a single trailing slash is tolerated.
//
// Fragments are always rejected (RFC 8707 section 2), as are relative or
// otherwise invalid URLs. Both arguments are expected in absolute URI form.
func resourceMatches(expected, actual string) bool {
	// A resource indicator must not contain a fragment; check the raw values
	// so that a bare trailing "#" is rejected too.
	if strings.ContainsRune(expected, '#') || strings.ContainsRune(actual, '#') {
		return false
	}
	eu, err := url.Parse(expected)
	if err != nil || !eu.IsAbs() || eu.Hostname() == "" || eu.User != nil {
		return false
	}
	au, err := url.Parse(actual)
	if err != nil || !au.IsAbs() || au.Hostname() == "" || au.User != nil {
		return false
	}
	if !strings.EqualFold(eu.Scheme, au.Scheme) ||
		!strings.EqualFold(eu.Hostname(), au.Hostname()) ||
		normalizedPort(eu) != normalizedPort(au) {
		return false
	}

	expectedPath := eu.EscapedPath()
	actualPath := au.EscapedPath()
	if expectedPath == "" {
		expectedPath = "/"
	}
	if actualPath == "" {
		actualPath = "/"
	}
	if !strings.HasPrefix(expectedPath, actualPath) {
		return false
	}
	if len(actualPath) == len(expectedPath) || strings.HasSuffix(actualPath, "/") {
		return true
	}
	// The next byte must start a new segment, so /api does not match /api123.
	return expectedPath[len(actualPath)] == '/'
}

// normalizedPort returns u's port with the scheme's default elided, matching
// URL-origin normalization (https://host:443 and https://host are the same
// origin).
func normalizedPort(u *url.URL) string {
	port := u.Port()
	switch {
	case port == "443" && strings.EqualFold(u.Scheme, "https"):
		return ""
	case port == "80" && strings.EqualFold(u.Scheme, "http"):
		return ""
	default:
		return port
	}
}
