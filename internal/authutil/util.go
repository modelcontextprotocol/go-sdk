// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package authutil

import "strings"

// IssuersEqual reports whether two OAuth 2.0 authorization server issuer
// identifiers refer to the same server. The comparison ignores a single
// trailing slash, matching the tolerance applied during RFC 8414 Section 3.3
// metadata validation.
//
// This helper is not appropriate for comparing the RFC 9207 iss response
// parameter, which the MCP authorization spec requires to be compared without
// any normalization.
func IssuersEqual(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}
