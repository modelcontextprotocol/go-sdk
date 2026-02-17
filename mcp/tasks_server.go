// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const relatedTaskMetaKey = "io.modelcontextprotocol/related-task"

type serverTasks struct {
	mu    sync.Mutex
	next  uint64
	tasks map[string]*serverTaskEntry
}

type serverTaskEntry struct {
	seq     uint64
	session *ServerSession
	meta    Meta
	args    []byte

	task      Task
	expiresAt *time.Time

	cancel context.CancelFunc
	done   chan struct{}

	result *CallToolResult
	err    error
}

func newServerTasks() *serverTasks {
	return &serverTasks{tasks: make(map[string]*serverTaskEntry)}
}

func (s *Server) tasksEnabledForToolsCall() bool {
	caps := s.capabilities()
	return caps.Tasks != nil &&
		caps.Tasks.Requests != nil &&
		caps.Tasks.Requests.Tools != nil &&
		caps.Tasks.Requests.Tools.Call != nil
}

func (s *Server) tasksEnabled() bool {
	return s.capabilities().Tasks != nil
}

func (s *Server) tasksListEnabled() bool {
	caps := s.capabilities()
	return caps.Tasks != nil && caps.Tasks.List != nil
}

func (s *Server) tasksCancelEnabled() bool {
	caps := s.capabilities()
	return caps.Tasks != nil && caps.Tasks.Cancel != nil
}

func (s *Server) callToolAny(ctx context.Context, req *CallToolRequest) (Result, error) {
	s.mu.Lock()
	st, ok := s.tools.get(req.Params.Name)
	s.mu.Unlock()
	if !ok {
		return nil, &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: fmt.Sprintf("unknown tool %q", req.Params.Name),
		}
	}

	// If the server hasn't advertised task augmentation for tools/call, ignore any
	// task request and process normally.
	if !s.tasksEnabledForToolsCall() {
		return s.callToolNow(ctx, req, st)
	}

	taskSupport := "forbidden"
	if st.tool.Execution != nil && st.tool.Execution.TaskSupport != "" {
		taskSupport = st.tool.Execution.TaskSupport
	}

	if req.Params.Task == nil {
		if taskSupport == "required" {
			return nil, fmt.Errorf("%w: task augmentation required for tools/call", jsonrpc2.ErrMethodNotFound)
		}
		return s.callToolNow(ctx, req, st)
	}

	// Task requested.
	if taskSupport == "forbidden" || taskSupport == "" {
		return nil, fmt.Errorf("%w: tool does not support task execution", jsonrpc2.ErrMethodNotFound)
	}
	if taskSupport != "optional" && taskSupport != "required" {
		return nil, fmt.Errorf("%w: invalid tool execution.taskSupport %q", jsonrpc2.ErrInvalidParams, taskSupport)
	}

	entry, err := s.tasks.createToolTask(req.Session, req.Params.Meta, req.Params.Arguments, req.Params.Task)
	if err != nil {
		return nil, err
	}

	// Run the tool asynchronously.
	go func() {
		defer func() {
			// Ensure we never leak a task wait.
			select {
			case <-entry.done:
			default:
				close(entry.done)
			}
		}()

		res, runErr := s.runToolTask(entry, st)

		s.tasks.finishToolTask(entry, res, runErr)
	}()

	t := entry.task // copy
	return &CreateTaskResult{Task: &t}, nil
}

func (s *Server) runToolTask(entry *serverTaskEntry, st *serverTool) (*CallToolResult, error) {
	// Tasks are durable relative to the initiating request lifetime.
	taskCtx, cancel := context.WithCancel(context.Background())
	s.tasks.setCancel(entry, cancel)
	defer cancel()

	paramsCopy := CallToolParamsRaw{
		Meta:      entry.meta,
		Name:      st.tool.Name,
		Arguments: append([]byte(nil), entry.args...),
		Task:      nil,
	}

	// The tool handler expects a CallToolRequest.
	toolReq := &CallToolRequest{Session: entry.session, Params: &paramsCopy}
	res, err := st.handler(taskCtx, toolReq)
	if err == nil && res != nil && res.Content == nil {
		res2 := *res
		res2.Content = []Content{}
		res = &res2
	}
	if err == nil && res == nil {
		res = &CallToolResult{Content: []Content{}}
	}
	return res, err
}

func (s *serverTasks) createToolTask(session *ServerSession, meta Meta, rawArgs []byte, tp *TaskParams) (*serverTaskEntry, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: missing session", jsonrpc2.ErrInvalidRequest)
	}
	if meta != nil {
		cp := make(Meta, len(meta))
		for k, v := range meta {
			cp[k] = v
		}
		meta = cp
	}

	now := time.Now().UTC()
	createdAt := now.Format(time.RFC3339)

	var ttl *int64
	var expiresAt *time.Time
	if tp != nil && tp.TTL != nil {
		v := *tp.TTL
		ttl = &v
		exp := now.Add(time.Duration(v) * time.Millisecond)
		expiresAt = &exp
	} else {
		// Explicitly include null TTL in tasks/get responses.
		ttl = nil
	}

	taskID, err := newTaskID()
	if err != nil {
		return nil, fmt.Errorf("%w: generating task id: %v", jsonrpc2.ErrInternal, err)
	}

	e := &serverTaskEntry{
		session: session,
		meta:    meta,
		args:    append([]byte(nil), rawArgs...),
		task: Task{
			Meta:          nil,
			TaskID:        taskID,
			Status:        TaskStatusWorking,
			StatusMessage: "The operation is now in progress.",
			CreatedAt:     createdAt,
			LastUpdatedAt: createdAt,
			TTL:           ttl,
		},
		expiresAt: expiresAt,
		done:      make(chan struct{}),
	}

	s.mu.Lock()
	s.next++
	e.seq = s.next
	s.tasks[taskID] = e
	s.mu.Unlock()

	return e, nil
}

func (s *serverTasks) setCancel(entry *serverTaskEntry, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.tasks[entry.task.TaskID]; ok {
		cur.cancel = cancel
	}
}

func (s *serverTasks) finishToolTask(entry *serverTaskEntry, res *CallToolResult, err error) {
	s.mu.Lock()
	cur := s.tasks[entry.task.TaskID]
	if cur == nil {
		s.mu.Unlock()
		return
	}
	cur.result = res
	cur.err = err

	// Respect terminal cancellation: do not transition away from cancelled.
	if cur.task.Status != TaskStatusCancelled {
		now := time.Now().UTC().Format(time.RFC3339)
		cur.task.LastUpdatedAt = now
		switch {
		case err != nil:
			cur.task.Status = TaskStatusFailed
			cur.task.StatusMessage = err.Error()
		case res != nil && res.IsError:
			cur.task.Status = TaskStatusFailed
			cur.task.StatusMessage = "tool execution failed"
		default:
			cur.task.Status = TaskStatusCompleted
			cur.task.StatusMessage = ""
		}
	}
	t := cur.task
	s.mu.Unlock()

	// Best-effort status notification.
	_ = handleNotify(context.Background(), notificationTaskStatus, newServerRequest(entry.session, (*TaskStatusNotificationParams)(&t)))
}

func (s *serverTasks) get(session *ServerSession, taskID string) (*serverTaskEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.tasks[taskID]
	if e == nil || e.session != session {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Failed to retrieve task: Task not found"}
	}
	if e.expiresAt != nil && time.Now().After(*e.expiresAt) {
		delete(s.tasks, taskID)
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Failed to retrieve task: Task has expired"}
	}
	return e, nil
}

func (s *Server) getTask(_ context.Context, req *GetTaskRequest) (*GetTaskResult, error) {
	if !s.tasksEnabled() {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	e, err := s.tasks.get(req.Session, req.Params.TaskID)
	if err != nil {
		return nil, err
	}
	t := GetTaskResult(e.task)
	return &t, nil
}

func (s *Server) listTasks(_ context.Context, req *ListTasksRequest) (*ListTasksResult, error) {
	if !s.tasksListEnabled() {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	if req.Params == nil {
		req.Params = &ListTasksParams{}
	}
	cursor, err := decodeTaskCursor(req.Params.Cursor)
	if err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Invalid cursor"}
	}

	entries := s.tasks.listForSession(req.Session)
	sort.Slice(entries, func(i, j int) bool { return entries[i].seq < entries[j].seq })

	start := 0
	if cursor != 0 {
		for i, e := range entries {
			if e.seq == cursor {
				start = i + 1
				break
			}
		}
		if start == 0 {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Invalid cursor"}
		}
	}

	pageSize := s.opts.PageSize
	end := start + pageSize
	if end > len(entries) {
		end = len(entries)
	}

	res := &ListTasksResult{Tasks: []*Task{}}
	for _, e := range entries[start:end] {
		t := e.task
		res.Tasks = append(res.Tasks, &t)
	}
	if end < len(entries) {
		res.NextCursor = encodeTaskCursor(entries[end-1].seq)
	}
	return res, nil
}

func (s *serverTasks) listForSession(session *ServerSession) []*serverTaskEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*serverTaskEntry
	now := time.Now()
	for id, e := range s.tasks {
		if e.session != session {
			continue
		}
		if e.expiresAt != nil && now.After(*e.expiresAt) {
			delete(s.tasks, id)
			continue
		}
		out = append(out, e)
	}
	return out
}

func (s *Server) cancelTask(_ context.Context, req *CancelTaskRequest) (*CancelTaskResult, error) {
	if !s.tasksCancelEnabled() {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	e, err := s.tasks.get(req.Session, req.Params.TaskID)
	if err != nil {
		return nil, err
	}

	// Terminal tasks cannot be cancelled.
	s.tasks.mu.Lock()
	cur := s.tasks.tasks[e.task.TaskID]
	if cur == nil {
		s.tasks.mu.Unlock()
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Failed to cancel task: Task not found"}
	}
	switch cur.task.Status {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
		s.tasks.mu.Unlock()
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("Cannot cancel task: already in terminal status %q", cur.task.Status)}
	default:
	}
	now := time.Now().UTC().Format(time.RFC3339)
	cur.task.Status = TaskStatusCancelled
	cur.task.StatusMessage = "The task was cancelled by request."
	cur.task.LastUpdatedAt = now
	cancel := cur.cancel
	t := cur.task
	s.tasks.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// Best-effort status notification.
	_ = handleNotify(context.Background(), notificationTaskStatus, newServerRequest(req.Session, (*TaskStatusNotificationParams)(&t)))

	res := CancelTaskResult(t)
	return &res, nil
}

func (s *Server) taskResult(ctx context.Context, req *TaskResultRequest) (*CallToolResult, error) {
	if !s.tasksEnabled() {
		return nil, jsonrpc2.ErrMethodNotFound
	}
	e, err := s.tasks.get(req.Session, req.Params.TaskID)
	if err != nil {
		return nil, err
	}

	<-e.done

	s.tasks.mu.Lock()
	cur := s.tasks.tasks[e.task.TaskID]
	res := cur.result
	err = cur.err
	s.tasks.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if res == nil {
		res = &CallToolResult{Content: []Content{}}
	}

	m := res.GetMeta()
	if m == nil {
		m = map[string]any{}
	}
	m[relatedTaskMetaKey] = map[string]any{"taskId": req.Params.TaskID}
	res.SetMeta(m)
	return res, nil
}

func (s *Server) callToolNow(ctx context.Context, req *CallToolRequest, st *serverTool) (*CallToolResult, error) {
	// Ensure tasks are not propagated into the underlying call.
	paramsCopy := *req.Params
	paramsCopy.Task = nil
	localReq := *req
	localReq.Params = &paramsCopy

	res, err := st.handler(ctx, &localReq)
	if err == nil && res != nil && res.Content == nil {
		res2 := *res
		res2.Content = []Content{} // avoid "null"
		res = &res2
	}
	return res, err
}

func newTaskID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Hex is fine; spec only requires a unique string.
	return hex.EncodeToString(b[:]), nil
}

func encodeTaskCursor(seq uint64) string {
	return strconv.FormatUint(seq, 10)
}

func decodeTaskCursor(cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	return strconv.ParseUint(cursor, 10, 64)
}
