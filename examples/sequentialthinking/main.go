// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var httpAddr = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")

// Thought represents a single step in the thinking process
type Thought struct {
	ID       int       `json:"id"`
	Content  string    `json:"content"`
	Created  time.Time `json:"created"`
	Revised  bool      `json:"revised"`
	ParentID *int      `json:"parentId,omitempty"` // For branching
}

// ThinkingSession represents an active thinking session
type ThinkingSession struct {
	ID             string    `json:"id"`
	Problem        string    `json:"problem"`
	Thoughts       []Thought `json:"thoughts"`
	CurrentThought int       `json:"currentThought"`
	EstimatedTotal int       `json:"estimatedTotal"`
	Status         string    `json:"status"` // "active", "completed", "paused"
	Created        time.Time `json:"created"`
	LastActivity   time.Time `json:"lastActivity"`
	Branches       []string  `json:"branches,omitempty"` // Alternative thought paths
}

// Global session store (in a real implementation, this might be a database)
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*ThinkingSession
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*ThinkingSession),
	}
}

func (s *SessionStore) GetSession(id string) (*ThinkingSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, exists := s.sessions[id]
	return session, exists
}

func (s *SessionStore) SetSession(session *ThinkingSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
}

func (s *SessionStore) ListSessions() []*ThinkingSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := make([]*ThinkingSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

var store = NewSessionStore()

// Tool argument structures
type StartThinkingArgs struct {
	Problem        string `json:"problem"`
	SessionID      string `json:"sessionId,omitempty"`
	EstimatedSteps int    `json:"estimatedSteps,omitempty"`
}

type ContinueThinkingArgs struct {
	SessionID      string `json:"sessionId"`
	Thought        string `json:"thought"`
	NextNeeded     *bool  `json:"nextNeeded,omitempty"`
	ReviseStep     *int   `json:"reviseStep,omitempty"`
	CreateBranch   *bool  `json:"createBranch,omitempty"`
	EstimatedTotal *int   `json:"estimatedTotal,omitempty"`
}

type ReviewThinkingArgs struct {
	SessionID string `json:"sessionId"`
}

type GetThinkingHistoryArgs struct {
	SessionID string `json:"sessionId"`
}

// Tool implementations
func StartThinking(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[StartThinkingArgs]) (*mcp.CallToolResultFor[any], error) {
	args := params.Arguments

	sessionID := args.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("session_%d", time.Now().Unix())
	}

	estimatedSteps := args.EstimatedSteps
	if estimatedSteps == 0 {
		estimatedSteps = 5 // Default estimate
	}

	session := &ThinkingSession{
		ID:             sessionID,
		Problem:        args.Problem,
		Thoughts:       []Thought{},
		CurrentThought: 0,
		EstimatedTotal: estimatedSteps,
		Status:         "active",
		Created:        time.Now(),
		LastActivity:   time.Now(),
		Branches:       []string{},
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

func ContinueThinking(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[ContinueThinkingArgs]) (*mcp.CallToolResultFor[any], error) {
	args := params.Arguments

	session, exists := store.GetSession(args.SessionID)
	if !exists {
		return nil, fmt.Errorf("session %s not found", args.SessionID)
	}

	session.LastActivity = time.Now()

	// Handle revision of existing thought
	if args.ReviseStep != nil {
		stepIndex := *args.ReviseStep - 1
		if stepIndex < 0 || stepIndex >= len(session.Thoughts) {
			return nil, fmt.Errorf("invalid step number: %d", *args.ReviseStep)
		}

		session.Thoughts[stepIndex].Content = args.Thought
		session.Thoughts[stepIndex].Revised = true

		store.SetSession(session)

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
	if args.CreateBranch != nil && *args.CreateBranch {
		branchID := fmt.Sprintf("%s_branch_%d", args.SessionID, len(session.Branches)+1)
		session.Branches = append(session.Branches, branchID)

		// Create a new session for the branch
		branchSession := &ThinkingSession{
			ID:             branchID,
			Problem:        session.Problem + " (Alternative branch)",
			Thoughts:       append([]Thought{}, session.Thoughts...), // Copy existing thoughts
			CurrentThought: len(session.Thoughts),
			EstimatedTotal: session.EstimatedTotal,
			Status:         "active",
			Created:        time.Now(),
			LastActivity:   time.Now(),
			Branches:       []string{},
		}

		store.SetSession(branchSession)
		session.LastActivity = time.Now()
		store.SetSession(session)

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
	thoughtID := len(session.Thoughts) + 1
	thought := Thought{
		ID:      thoughtID,
		Content: args.Thought,
		Created: time.Now(),
		Revised: false,
	}

	session.Thoughts = append(session.Thoughts, thought)
	session.CurrentThought = thoughtID

	// Update estimated total if provided
	if args.EstimatedTotal != nil {
		session.EstimatedTotal = *args.EstimatedTotal
	}

	// Check if thinking is complete
	if args.NextNeeded != nil && !*args.NextNeeded {
		session.Status = "completed"
	}

	store.SetSession(session)

	progress := fmt.Sprintf("Step %d", thoughtID)
	if session.EstimatedTotal > 0 {
		progress += fmt.Sprintf(" of ~%d", session.EstimatedTotal)
	}

	statusMsg := ""
	if session.Status == "completed" {
		statusMsg = "\n✓ Thinking process completed!"
	} else {
		statusMsg = "\nReady for next thought..."
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

func ReviewThinking(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[ReviewThinkingArgs]) (*mcp.CallToolResultFor[any], error) {
	args := params.Arguments

	session, exists := store.GetSession(args.SessionID)
	if !exists {
		return nil, fmt.Errorf("session %s not found", args.SessionID)
	}

	var review strings.Builder
	review.WriteString(fmt.Sprintf("=== Thinking Review: %s ===\n", session.ID))
	review.WriteString(fmt.Sprintf("Problem: %s\n", session.Problem))
	review.WriteString(fmt.Sprintf("Status: %s\n", session.Status))
	review.WriteString(fmt.Sprintf("Steps: %d of ~%d\n", len(session.Thoughts), session.EstimatedTotal))

	if len(session.Branches) > 0 {
		review.WriteString(fmt.Sprintf("Branches: %s\n", strings.Join(session.Branches, ", ")))
	}

	review.WriteString("\n--- Thought Sequence ---\n")

	for i, thought := range session.Thoughts {
		status := ""
		if thought.Revised {
			status = " (revised)"
		}
		review.WriteString(fmt.Sprintf("%d. %s%s\n", i+1, thought.Content, status))
	}

	return &mcp.CallToolResultFor[any]{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: review.String(),
			},
		},
	}, nil
}

// Resource handler for thinking sessions
func GetThinkingHistory(ctx context.Context, ss *mcp.ServerSession, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	// Extract session ID from URI (e.g., "thinking://session_123")
	parts := strings.Split(params.URI, "://")
	if len(parts) != 2 || parts[0] != "thinking" {
		return nil, fmt.Errorf("invalid thinking resource URI: %s", params.URI)
	}

	sessionID := parts[1]
	if sessionID == "sessions" {
		// List all sessions
		sessions := store.ListSessions()
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

	// Get specific session
	session, exists := store.GetSession(sessionID)
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

func main() {
	flag.Parse()

	server := mcp.NewServer("sequential-thinking", "v0.0.1", nil)

	// Add thinking tools
	server.AddTools(
		mcp.NewServerTool(
			"start_thinking",
			"Begin a new sequential thinking session for a complex problem",
			StartThinking,
		),
		mcp.NewServerTool(
			"continue_thinking",
			"Add the next thought step, revise a previous step, or create a branch",
			ContinueThinking,
		),
		mcp.NewServerTool(
			"review_thinking",
			"Review the complete thinking process for a session",
			ReviewThinking,
		),
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
			Handler: GetThinkingHistory,
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

