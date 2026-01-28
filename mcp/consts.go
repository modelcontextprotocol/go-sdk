// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

const (
	ElicitActionAccept  = "accept"  // User submitted the form/confirmed the action
	ElicitActionDecline = "decline" // User explicitly declined the action
	ElicitActionCancel  = "cancel"  // User dismissed without making an explicit choice
)
