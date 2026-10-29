// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package authutil

import (
	"fmt"
	"slices"
)

// CheckPKCESupport returns an error unless methods, the
// code_challenge_methods_supported of the authorization server at issuer,
// includes S256, the PKCE method that the SDK's authorization code flows use.
// The MCP authorization spec requires a client to refuse an authorization
// server that does not advertise PKCE before starting such a flow.
func CheckPKCESupport(methods []string, issuer string) error {
	if len(methods) == 0 {
		return fmt.Errorf("authorization server at %s does not implement PKCE", issuer)
	}
	if !slices.Contains(methods, "S256") {
		return fmt.Errorf("authorization server at %s does not support the S256 PKCE method", issuer)
	}
	return nil
}
