// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")

// A Thought is a single step in the thinking process.
type Thought struct {
	// Index of the thought within the session (1-based).
	Index int `json:"index"`
	// Content of the thought.
	Content string `json:"content"`
	// Time the thought was created.
	Created time.Time `json:"created"`
	// Whether the thought has been revised.
	Revised bool `json:"revised"`
	// Index of parent thought, or nil if this is a root for branching.
	ParentIndex *int `json:"parentIndex,omitempty"`
}

// A ThinkingSession is an active thinking session.
type ThinkingSession struct {
	// Globally unique ID of the session.
	ID string `json:"id"`
	// Problem to solve.
	Problem string `json:"problem"`
	// Thoughts in the session.
	Thoughts []*Thought `json:"thoughts"`
	// Current thought index.
	CurrentThought int `json:"currentThought"`
	// Estimated total number of thoughts.
	EstimatedTotal int `json:"estimatedTotal"`
	// Status of the session.
	Status string `json:"status"` // "active", "completed", "paused"
	// Time the session was created.
	Created time.Time `json:"created"`
	// Time the session was last active.
	LastActivity time.Time `json:"lastActivity"`
	// Branches in the session. Alternative thought paths.
	Branches []string `json:"branches,omitempty"`
	// Version for optimistic concurrency control.
	Version int `json:"version"`
}

// A SessionStore is a global session store (in a real implementation, this might be a database).
//
// Locking Strategy:
// The SessionStore uses a RWMutex to protect the sessions map from concurrent access.
// All ThinkingSession modifications happen on deep copies, never on shared instances.
// This means:
// - Read locks protect map access (reading from a Go map during writes causes panics)
// - Write locks protect map modifications (adding/removing/replacing sessions)
// - Session field modifications always happen on local copies via CompareAndSwap
// - No shared ThinkingSession state is ever modified directly
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*ThinkingSession // key is session ID
}

// NewSessionStore creates a new session store for managing thinking sessions.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*ThinkingSession),
	}
}

// Session retrieves a thinking session by ID, returning the session and whether it exists.
func (s *SessionStore) Session(id string) (*ThinkingSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, exists := s.sessions[id]
	return session, exists
}

// SetSession stores or updates a thinking session in the store.
func (s *SessionStore) SetSession(session *ThinkingSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
}

// CompareAndSwap atomically updates a session if the version matches.
// Returns true if the update succeeded, false if there was a version mismatch.
//
// This method implements optimistic concurrency control:
// 1. Read lock to safely access the map and copy the session
// 2. Deep copy the session (all modifications happen on this copy)
// 3. Release read lock and apply updates to the copy
// 4. Write lock to check version and atomically update if unchanged
//
// The read lock in step 1 is necessary to prevent map access races,
// not to protect ThinkingSession fields (which are never modified in-place).
func (s *SessionStore) CompareAndSwap(sessionID string, updateFunc func(*ThinkingSession) (*ThinkingSession, error)) error {
	for {
		// Get current session
		s.mu.RLock()
		current, exists := s.sessions[sessionID]
		if !exists {
			s.mu.RUnlock()
			return fmt.Errorf("session %s not found", sessionID)
		}
		// Create a deep copy
		sessionCopy := *current
		sessionCopy.Thoughts = slices.Clone(current.Thoughts)
		for i, thought := range current.Thoughts {
			thoughtCopy := *thought
			sessionCopy.Thoughts[i] = &thoughtCopy
		}
		sessionCopy.Branches = slices.Clone(current.Branches)
		oldVersion := current.Version
		s.mu.RUnlock()

		// Apply the update
		updated, err := updateFunc(&sessionCopy)
		if err != nil {
			return err
		}

		// Try to save
		s.mu.Lock()
		current, exists = s.sessions[sessionID]
		if !exists {
			s.mu.Unlock()
			return fmt.Errorf("session %s not found", sessionID)
		}
		if current.Version != oldVersion {
			// Version mismatch, retry
			s.mu.Unlock()
			continue
		}
		updated.Version = oldVersion + 1
		s.sessions[sessionID] = updated
		s.mu.Unlock()
		return nil
	}
}

// Sessions returns all thinking sessions in the store.
func (s *SessionStore) Sessions() []*ThinkingSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Collect(maps.Values(s.sessions))
}

// SessionsSnapshot returns a deep copy of all sessions for safe concurrent access.
func (s *SessionStore) SessionsSnapshot() []*ThinkingSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]*ThinkingSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		// Create a deep copy of each session
		sessionCopy := *session
		sessionCopy.Thoughts = deepCopyThoughts(session.Thoughts)
		sessionCopy.Branches = slices.Clone(session.Branches)
		sessions = append(sessions, &sessionCopy)
	}
	return sessions
}

// SessionSnapshot returns a deep copy of a session for safe concurrent access.
func (s *SessionStore) SessionSnapshot(id string) (*ThinkingSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[id]
	if !exists {
		return nil, false
	}

	// Create a deep copy
	sessionCopy := *session
	sessionCopy.Thoughts = deepCopyThoughts(session.Thoughts)
	sessionCopy.Branches = slices.Clone(session.Branches)
	return &sessionCopy, true
}

var store = NewSessionStore()

// StartThinkingArgs are the arguments for starting a new thinking session.
type StartThinkingArgs struct {
	Problem        string `json:"problem"`
	SessionID      string `json:"sessionId,omitempty"`
	EstimatedSteps int    `json:"estimatedSteps,omitempty"`
}

// ContinueThinkingArgs are the arguments for continuing a thinking session.
type ContinueThinkingArgs struct {
	SessionID      string `json:"sessionId"`
	Thought        string `json:"thought"`
	NextNeeded     *bool  `json:"nextNeeded,omitempty"`
	ReviseStep     *int   `json:"reviseStep,omitempty"`
	CreateBranch   bool   `json:"createBranch,omitempty"`
	EstimatedTotal int    `json:"estimatedTotal,omitempty"`
}

// ReviewThinkingArgs are the arguments for reviewing a thinking session.
type ReviewThinkingArgs struct {
	SessionID string `json:"sessionId"`
}

// ThinkingHistoryArgs are the arguments for retrieving thinking history.
type ThinkingHistoryArgs struct {
	SessionID string `json:"sessionId"`
}

// deepCopyThoughts creates a deep copy of a slice of thoughts.
func deepCopyThoughts(thoughts []*Thought) []*Thought {
	thoughtsCopy := make([]*Thought, len(thoughts))
	for i, t := range thoughts {
		thoughtCopy := *t
		thoughtsCopy[i] = &thoughtCopy
	}
	return thoughtsCopy
}

// StartThinking begins a new sequential thinking session for a complex problem.
func StartThinking(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[StartThinkingArgs]) (*mcp.CallToolResultFor[any], error) {
	args := params.Arguments

	sessionID := args.SessionID
	if sessionID == "" {
		sessionID = randText()
	}

	estimatedSteps := args.EstimatedSteps
	if estimatedSteps == 0 {
		estimatedSteps = 5 // Default estimate
	}

	session := &ThinkingSession{
		ID:             sessionID,
		Problem:        args.Problem,
		EstimatedTotal: estimatedSteps,
		Status:         "active",
		Created:        time.Now(),
		LastActivity:   time.Now(),
	}

	store.SetSession(session)

	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Started thinking session '%s' for problem: %s\nEstimated steps: %d\nReady for your first thought.",
					sessionID, args.Problem, estimatedSteps),
			},
		},
	}, nil
}

// ContinueThinking adds the next thought step, revises a previous step, or creates a branch in the thinking process.
func ContinueThinking(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[ContinueThinkingArgs]) (*mcp.CallToolResultFor[any], error) {
	args := params.Arguments

	// Handle revision of existing thought
	if args.ReviseStep != nil {
		err := store.CompareAndSwap(args.SessionID, func(session *ThinkingSession) (*ThinkingSession, error) {
			stepIndex := *args.ReviseStep - 1
			if stepIndex < 0 || stepIndex >= len(session.Thoughts) {
				return nil, fmt.Errorf("invalid step number: %d", *args.ReviseStep)
			}

			session.Thoughts[stepIndex].Content = args.Thought
			session.Thoughts[stepIndex].Revised = true
			session.LastActivity = time.Now()
			return session, nil
		})
		if err != nil {
			return nil, err
		}

		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Revised step %d in session '%s':\n%s",
						*args.ReviseStep, args.SessionID, args.Thought),
				},
			},
		}, nil
	}

	// Handle branching
	if args.CreateBranch {
		var branchID string
		var branchSession *ThinkingSession

		err := store.CompareAndSwap(args.SessionID, func(session *ThinkingSession) (*ThinkingSession, error) {
			branchID = fmt.Sprintf("%s_branch_%d", args.SessionID, len(session.Branches)+1)
			session.Branches = append(session.Branches, branchID)
			session.LastActivity = time.Now()

			// Create a new session for the branch (deep copy thoughts)
			thoughtsCopy := deepCopyThoughts(session.Thoughts)
			branchSession = &ThinkingSession{
				ID:             branchID,
				Problem:        session.Problem + " (Alternative branch)",
				Thoughts:       thoughtsCopy,
				CurrentThought: len(session.Thoughts),
				EstimatedTotal: session.EstimatedTotal,
				Status:         "active",
				Created:        time.Now(),
				LastActivity:   time.Now(),
			}

			return session, nil
		})
		if err != nil {
			return nil, err
		}

		// Save the branch session
		store.SetSession(branchSession)

		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Created branch '%s' from session '%s'. You can now continue thinking in either session.",
						branchID, args.SessionID),
				},
			},
		}, nil
	}

	// Add new thought
	var thoughtID int
	var progress string
	var statusMsg string

	err := store.CompareAndSwap(args.SessionID, func(session *ThinkingSession) (*ThinkingSession, error) {
		thoughtID = len(session.Thoughts) + 1
		thought := &Thought{
			Index:   thoughtID,
			Content: args.Thought,
			Created: time.Now(),
			Revised: false,
		}

		session.Thoughts = append(session.Thoughts, thought)
		session.CurrentThought = thoughtID
		session.LastActivity = time.Now()

		// Update estimated total if provided
		if args.EstimatedTotal > 0 {
			session.EstimatedTotal = args.EstimatedTotal
		}

		// Check if thinking is complete
		if args.NextNeeded != nil && !*args.NextNeeded {
			session.Status = "completed"
		}

		// Prepare response strings
		progress = fmt.Sprintf("Step %d", thoughtID)
		if session.EstimatedTotal > 0 {
			progress += fmt.Sprintf(" of ~%d", session.EstimatedTotal)
		}

		if session.Status == "completed" {
			statusMsg = "\n✓ Thinking process completed!"
		} else {
			statusMsg = "\nReady for next thought..."
		}

		return session, nil
	})
	if err != nil {
		return nil, err
	}

	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("Session '%s' - %s:\n%s%s",
					args.SessionID, progress, args.Thought, statusMsg),
			},
		},
	}, nil
}

// ReviewThinking provides a complete review of the thinking process for a session.
func ReviewThinking(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[ReviewThinkingArgs]) (*mcp.CallToolResultFor[any], error) {
	args := params.Arguments

	// Get a snapshot of the session to avoid race conditions
	store.mu.RLock()
	session, exists := store.sessions[args.SessionID]
	if !exists {
		store.mu.RUnlock()
		return nil, fmt.Errorf("session %s not found", args.SessionID)
	}
	// Create a snapshot of the data we need while holding the lock
	sessionSnapshot := struct {
		ID             string
		Problem        string
		Status         string
		EstimatedTotal int
		Branches       []string
		Thoughts       []*Thought
	}{
		ID:             session.ID,
		Problem:        session.Problem,
		Status:         session.Status,
		EstimatedTotal: session.EstimatedTotal,
		Branches:       slices.Clone(session.Branches),
		Thoughts:       deepCopyThoughts(session.Thoughts),
	}
	store.mu.RUnlock()

	var review strings.Builder
	fmt.Fprintf(&review, "=== Thinking Review: %s ===\n", sessionSnapshot.ID)
	fmt.Fprintf(&review, "Problem: %s\n", sessionSnapshot.Problem)
	fmt.Fprintf(&review, "Status: %s\n", sessionSnapshot.Status)
	fmt.Fprintf(&review, "Steps: %d of ~%d\n", len(sessionSnapshot.Thoughts), sessionSnapshot.EstimatedTotal)

	if len(sessionSnapshot.Branches) > 0 {
		fmt.Fprintf(&review, "Branches: %s\n", strings.Join(sessionSnapshot.Branches, ", "))
	}

	fmt.Fprintf(&review, "\n--- Thought Sequence ---\n")

	for i, thought := range sessionSnapshot.Thoughts {
		status := ""
		if thought.Revised {
			status = " (revised)"
		}
		fmt.Fprintf(&review, "%d. %s%s\n", i+1, thought.Content, status)
	}

	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: review.String(),
			},
		},
	}, nil
}

// ThinkingHistory handles resource requests for thinking session data and history.
func ThinkingHistory(ctx context.Context, ss *mcp.ServerSession, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	// Extract session ID from URI (e.g., "thinking://session_123")
	u, err := url.Parse(params.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid thinking resource URI: %s", params.URI)
	}
	if u.Scheme != "thinking" {
		return nil, fmt.Errorf("invalid thinking resource URI scheme: %s", u.Scheme)
	}

	sessionID := u.Host
	if sessionID == "sessions" {
		// List all sessions - use snapshot for thread safety
		sessions := store.SessionsSnapshot()
		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal sessions: %w", err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      params.URI,
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	}

	// Get specific session - use snapshot for thread safety
	session, exists := store.SessionSnapshot(sessionID)
	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal session: %w", err)
	}

	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      params.URI,
				MIMEType: "application/json",
				Text:     string(data),
			},
		},
	}, nil
}

// Copied from crypto/rand.
// TODO: once 1.24 is assured, just use crypto/rand.
const base32alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

func randText() string {
	// ⌈log₃₂ 2¹²⁸⌉ = 26 chars
	src := make([]byte, 26)
	rand.Read(src)
	for i := range src {
		src[i] = base32alphabet[src[i]%32]
	}
	return string(src)
}

func main() {
	flag.Parse()

	server := mcp.NewServer("sequential-thinking", "v0.0.1", nil)

	// Add thinking tools without output schemas
	startThinkingSchema, err := jsonschema.For[StartThinkingArgs]()
	if err != nil {
		log.Fatalf("Failed to create start_thinking schema: %v", err)
	}
	continueThinkingSchema, err := jsonschema.For[ContinueThinkingArgs]()
	if err != nil {
		log.Fatalf("Failed to create continue_thinking schema: %v", err)
	}
	reviewThinkingSchema, err := jsonschema.For[ReviewThinkingArgs]()
	if err != nil {
		log.Fatalf("Failed to create review_thinking schema: %v", err)
	}

	server.AddTools(
		&mcp.ServerTool{
			Tool: &mcp.Tool{
				Name:        "start_thinking",
				Description: "Begin a new sequential thinking session for a complex problem",
				InputSchema: startThinkingSchema,
				// No OutputSchema to avoid structured output requirement
			},
			Handler: func(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[map[string]any]) (*mcp.CallToolResult, error) {
				// Convert map[string]any to StartThinkingArgs
				args := StartThinkingArgs{}
				if v, ok := params.Arguments["problem"].(string); ok {
					args.Problem = v
				}
				if v, ok := params.Arguments["sessionId"].(string); ok {
					args.SessionID = v
				}
				if v, ok := params.Arguments["estimatedSteps"].(float64); ok {
					args.EstimatedSteps = int(v)
				}

				result, err := StartThinking(ctx, ss, &mcp.CallToolParamsFor[StartThinkingArgs]{
					Meta:      params.Meta,
					Name:      params.Name,
					Arguments: args,
				})
				if err != nil {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
						IsError: true,
					}, nil
				}
				return &mcp.CallToolResult{
					Content: result.Content,
					IsError: result.IsError,
				}, nil
			},
		},
		&mcp.ServerTool{
			Tool: &mcp.Tool{
				Name:        "continue_thinking",
				Description: "Add the next thought step, revise a previous step, or create a branch",
				InputSchema: continueThinkingSchema,
				// No OutputSchema to avoid structured output requirement
			},
			Handler: func(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[map[string]any]) (*mcp.CallToolResult, error) {
				// Convert map[string]any to ContinueThinkingArgs
				args := ContinueThinkingArgs{}
				if v, ok := params.Arguments["sessionId"].(string); ok {
					args.SessionID = v
				}
				if v, ok := params.Arguments["thought"].(string); ok {
					args.Thought = v
				}
				if v, ok := params.Arguments["nextNeeded"].(bool); ok {
					args.NextNeeded = &v
				}
				if v, ok := params.Arguments["reviseStep"].(float64); ok {
					step := int(v)
					args.ReviseStep = &step
				}
				if v, ok := params.Arguments["createBranch"].(bool); ok {
					args.CreateBranch = v
				}
				if v, ok := params.Arguments["estimatedTotal"].(float64); ok {
					args.EstimatedTotal = int(v)
				}

				result, err := ContinueThinking(ctx, ss, &mcp.CallToolParamsFor[ContinueThinkingArgs]{
					Meta:      params.Meta,
					Name:      params.Name,
					Arguments: args,
				})
				if err != nil {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
						IsError: true,
					}, nil
				}
				return &mcp.CallToolResult{
					Content: result.Content,
					IsError: result.IsError,
				}, nil
			},
		},
		&mcp.ServerTool{
			Tool: &mcp.Tool{
				Name:        "review_thinking",
				Description: "Review the complete thinking process for a session",
				InputSchema: reviewThinkingSchema,
				// No OutputSchema to avoid structured output requirement
			},
			Handler: func(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[map[string]any]) (*mcp.CallToolResult, error) {
				// Convert map[string]any to ReviewThinkingArgs
				args := ReviewThinkingArgs{}
				if v, ok := params.Arguments["sessionId"].(string); ok {
					args.SessionID = v
				}

				result, err := ReviewThinking(ctx, ss, &mcp.CallToolParamsFor[ReviewThinkingArgs]{
					Meta:      params.Meta,
					Name:      params.Name,
					Arguments: args,
				})
				if err != nil {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
						IsError: true,
					}, nil
				}
				return &mcp.CallToolResult{
					Content: result.Content,
					IsError: result.IsError,
				}, nil
			},
		},
	)

	// Add resources for accessing thinking history
	server.AddResources(
		&mcp.ServerResource{
			Resource: &mcp.Resource{
				Name:        "thinking_sessions",
				Description: "Access thinking session data and history",
				URI:         "thinking://sessions",
				MIMEType:    "application/json",
			},
			Handler: ThinkingHistory,
		},
	)

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("Sequential Thinking MCP server listening at %s", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, handler); err != nil {
			log.Fatal(err)
		}
	} else {
		t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
		if err := server.Run(context.Background(), t); err != nil {
			log.Printf("Server failed: %v", err)
		}
	}
}
