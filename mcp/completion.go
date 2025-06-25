// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

type Reference struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string ` json:"uri,omitempty"`
}

func (r *Reference) UnmarshalJSON(data []byte) error {
	type wireReference Reference // for naive unmarshaling
	var r2 wireReference
	if err := json.Unmarshal(data, &r2); err != nil {
		return err
	}
	switch r2.Type {
	case "ref/prompt", "ref/resource":
	default:
		return fmt.Errorf("unrecognized content type %s", r2.Type)
	}
	*r = Reference(r2)
	return nil
}

// A CompletionHandler handles completion.
type CompletionHandler func(context.Context, *ServerSession, *CompleteParams) (*CompleteResult, error)
