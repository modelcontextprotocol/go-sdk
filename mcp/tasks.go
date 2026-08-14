// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import "fmt"

// ExtensionTasks identifies the MCP Tasks extension, which lets a server answer
// a request with a durable task handle instead of the request's normal result.
//
// This SDK does not implement task execution, and does not declare the
// extension by default: declaring it obliges a client to poll a task handle to
// completion, and a server to serve the tasks/* methods.
//
// See https://github.com/modelcontextprotocol/ext-tasks.
const ExtensionTasks = "io.modelcontextprotocol/tasks"

// UnsupportedTaskResultError reports that a peer answered a request with a task
// handle from the [ExtensionTasks] extension, which this SDK cannot resolve.
type UnsupportedTaskResultError struct {
	// TaskID identifies the created task, for manual polling or cancellation.
	TaskID string
}

func (e *UnsupportedTaskResultError) Error() string {
	return fmt.Sprintf("peer created task %q: the %s extension is not implemented", e.TaskID, ExtensionTasks)
}
