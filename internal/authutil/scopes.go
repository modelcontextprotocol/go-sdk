// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.
package authutil

import (
	"maps"
	"slices"
	"strings"

	"golang.org/x/oauth2"
)

// UnionScopes returns the union of the existing and challenged scope sets.
// It is used during step-up authorization to accumulate scopes across
// authorization rounds (SEP-2350).
func UnionScopes(existing, challenged []string) []string {
	combined := make(map[string]struct{})
	for _, s := range existing {
		combined[s] = struct{}{}
	}
	for _, s := range challenged {
		combined[s] = struct{}{}
	}
	return slices.Collect(maps.Keys(combined))
}

// ScopesFromToken extracts the granted scopes from an OAuth2 token response.
// Per RFC 6749 §5.1, the scope parameter is optional; returns nil if absent.
// An empty scope names no scope-token, so it is treated as absent too:
// callers take nil to mean the requested scopes were granted.
func ScopesFromToken(token *oauth2.Token) []string {
	scope, ok := token.Extra("scope").(string)
	if !ok {
		return nil
	}
	scopes := strings.Fields(scope)
	if len(scopes) == 0 {
		return nil
	}
	return scopes
}
