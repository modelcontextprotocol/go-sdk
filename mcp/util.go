// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"crypto/rand"
	"encoding/json"
)

func assert(cond bool, msg string) {
	if !cond {
		panic(msg)
	}
}

func randText() string {
	return rand.Text()
}

// remarshal marshals from to JSON, and then unmarshals into to, which must be
// a pointer type.
func remarshal(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, to); err != nil {
		return err
	}
	return nil
}
