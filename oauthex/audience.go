// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import "strings"

// MatchesResource reports whether any of `claims` matches `resource` under
// the canonical-form (trailing slash) tolerance required by RFC 9728
// (Protected Resource Metadata) and RFC 8707 (Resource Indicators). Both
// canonical and non-trailing-slash forms compare equal.
//
// Background: RFC 9728 §3.3 canonicalises the protected-resource identifier
// with a trailing slash, but RFC 8707 resource indicators sometimes omit it,
// and upstream IdPs vary in which form they emit in `aud` claims (Google
// trims, Auth0 retains, claude.ai round-trips whichever it received). Strict
// byte equality therefore fails routinely on legitimate setups. This helper
// matches both forms while preserving exact-equality semantics — leading
// whitespace, schemes, paths must still match.
//
// Returns false when claims is empty.
func MatchesResource(claims []string, resource string) bool {
	expected := strings.TrimRight(strings.TrimSpace(resource), "/")
	for _, c := range claims {
		if c == resource {
			return true
		}
		if strings.TrimRight(strings.TrimSpace(c), "/") == expected {
			return true
		}
	}
	return false
}
