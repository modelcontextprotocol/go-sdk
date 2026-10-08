// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp

import (
	"crypto/rand"
	"fmt"
)

// ExtensionTasks identifies the MCP Tasks extension, which lets a server answer
// a request with a durable task handle instead of the request's normal result.
//
// This SDK does not implement task execution, and does not declare the
// extension by default: declaring it obliges a client to poll a task handle to
// completion, and a server to serve the tasks/* methods.
//
// See https://github.com/modelcontextprotocol/ext-tasks/blob/main/specification/draft/tasks.md.
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

// TaskStatus is the state of a task in the [ExtensionTasks] extension.
// Values outside the constants below are preserved: the extension may add
// statuses, and a peer's status must round-trip unchanged.
//
// See https://github.com/modelcontextprotocol/ext-tasks/blob/main/specification/draft/tasks.md.
type TaskStatus string

const (
	// TaskStatusWorking means the request is currently being processed.
	TaskStatusWorking TaskStatus = "working"
	// TaskStatusInputRequired means the server needs input from the client.
	TaskStatusInputRequired TaskStatus = "input_required"
	// TaskStatusCompleted means the request finished and its result is available.
	TaskStatusCompleted TaskStatus = "completed"
	// TaskStatusCancelled means the request was cancelled before completion.
	TaskStatusCancelled TaskStatus = "cancelled"
	// TaskStatusFailed means the request failed with a JSON-RPC error.
	TaskStatusFailed TaskStatus = "failed"
)

// Task is the operational metadata for a task in the [ExtensionTasks] extension.
// Derived shapes that carry inputRequests, result, or error are not modeled
// here; those belong to task execution, which this SDK does not implement.
//
// Timestamps are strings, matching [Annotations.LastModified]: the extension
// types them as ISO 8601 text, and parsing them as time.Time would reject
// forms the spec allows and rewrite the value on the way back out.
//
// See https://github.com/modelcontextprotocol/ext-tasks/blob/main/specification/draft/tasks.md.
type Task struct {
	// TaskID is the server-generated identifier for this task.
	TaskID string `json:"taskId"`
	// Status is the current task state.
	Status TaskStatus `json:"status"`
	// StatusMessage is an optional description of the current state.
	StatusMessage string `json:"statusMessage,omitempty"`
	// CreatedAt is the ISO 8601 timestamp when the task was created.
	CreatedAt string `json:"createdAt"`
	// LastUpdatedAt is the ISO 8601 timestamp when the task was last updated.
	LastUpdatedAt string `json:"lastUpdatedAt"`
	// TTLMs is the time-to-live from creation, in milliseconds.
	// Nil encodes as JSON null, which the extension defines as unlimited.
	// The field is required, so a nil pointer is sent as null. int64 holds
	// a TTL past the 32-bit range: a year is about 3.15e10 ms.
	TTLMs *int64 `json:"ttlMs"`
	// PollIntervalMs is the suggested polling interval in milliseconds.
	// Omitted when unset.
	PollIntervalMs *int64 `json:"pollIntervalMs,omitempty"`
}

// newTaskID returns an unguessable task ID. The extension requires
// server-generated IDs with enough entropy that a third party cannot
// enumerate them. [crypto/rand.Text] supplies at least 128 bits.
func newTaskID() string {
	return rand.Text()
}
