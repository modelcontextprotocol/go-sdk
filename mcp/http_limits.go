// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"errors"
	"net/http"
)

// DefaultMaxBodyBytes is the default maximum size (in bytes) for HTTP request
// bodies accepted by the built-in SSE and streamable HTTP handlers.
//
// This limit exists to prevent accidental or malicious large requests from
// exhausting server resources.
const DefaultMaxBodyBytes int64 = 1_000_000

// effectiveMaxBodyBytes converts the user-configured maxBodyBytes value to an
// effective limit.
//
// Semantics:
//   - maxBodyBytes == 0: use DefaultMaxBodyBytes
//   - maxBodyBytes  < 0: no limit
//   - maxBodyBytes  > 0: use maxBodyBytes
func effectiveMaxBodyBytes(maxBodyBytes int64) int64 {
	switch {
	case maxBodyBytes == 0:
		return DefaultMaxBodyBytes
	case maxBodyBytes < 0:
		return 0
	default:
		return maxBodyBytes
	}
}

func isMaxBytesError(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

func writeRequestBodyTooLarge(w http.ResponseWriter) {
	// Even though http.MaxBytesReader will try to close the connection after the
	// limit is exceeded, explicitly request closure here too.
	w.Header().Set("Connection", "close")
	http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
}
