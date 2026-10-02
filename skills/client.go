// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"fmt"
	"iter"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddClient registers the Skills extension methods that client may send.
func AddClient(client *mcp.Client) error {
	if client == nil {
		return fmt.Errorf("skills: nil client")
	}
	if err := mcp.AddSendingCustomMethod[*ListSkillsParams, *ListSkillsResult](client, MethodList); err != nil {
		return err
	}
	if err := mcp.AddSendingCustomMethod[*GetSkillParams, *GetSkillResult](client, MethodGet); err != nil {
		return err
	}
	return mcp.AddSendingCustomMethod[*ReadDirectoryParams, *ReadDirectoryResult](client, MethodReadDirectory)
}

// List calls skills/list and validates the response.
func List(ctx context.Context, session *mcp.ClientSession, params *ListSkillsParams) (*ListSkillsResult, error) {
	if err := requireCapability(session, false); err != nil {
		return nil, err
	}
	if params == nil {
		params = &ListSkillsParams{}
	}
	result, err := mcp.CallCustomMethod[*ListSkillsParams, *ListSkillsResult](ctx, session, MethodList, params)
	if err != nil {
		return nil, err
	}
	if err := validateListResponse(ctx, result); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid skills/list result: %w", err)
	}
	return result, nil
}

// Get calls skills/get and validates the response.
func Get(ctx context.Context, session *mcp.ClientSession, params *GetSkillParams) (*GetSkillResult, error) {
	if err := requireCapability(session, false); err != nil {
		return nil, err
	}
	if params == nil || params.URI == "" {
		return nil, fmt.Errorf("skills: get requires a URI")
	}
	result, err := mcp.CallCustomMethod[*GetSkillParams, *GetSkillResult](ctx, session, MethodGet, params)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Skill == nil {
		return nil, fmt.Errorf("skills: server returned a nil skill")
	}
	if result.Skill.URI != params.URI {
		return nil, fmt.Errorf("skills: server returned URI %q for %q", result.Skill.URI, params.URI)
	}
	if err := ValidateSkill(result.Skill); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid skill: %w", err)
	}
	return result, nil
}

// ReadDirectory calls resources/directory/read and validates the response.
func ReadDirectory(ctx context.Context, session *mcp.ClientSession, params *ReadDirectoryParams) (*ReadDirectoryResult, error) {
	if err := requireCapability(session, true); err != nil {
		return nil, err
	}
	if params == nil || params.URI == "" {
		return nil, fmt.Errorf("skills: directory read requires a URI")
	}
	result, err := mcp.CallCustomMethod[*ReadDirectoryParams, *ReadDirectoryResult](ctx, session, MethodReadDirectory, params)
	if err != nil {
		return nil, err
	}
	if err := ValidateDirectoryResult(params.URI, result); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid directory result: %w", err)
	}
	return result, nil
}

// All returns an iterator that follows every page of skills/list.
func All(ctx context.Context, session *mcp.ClientSession, params *ListSkillsParams) iter.Seq2[*Skill, error] {
	var initial ListSkillsParams
	if params != nil {
		initial = *params
		initial.Meta = maps.Clone(params.Meta)
	}
	return func(yield func(*Skill, error) bool) {
		request := initial
		allPages(initial.Cursor, func(cursor string) ([]*Skill, string, error) {
			request.Cursor = cursor
			result, err := List(ctx, session, &request)
			if err != nil {
				return nil, "", err
			}
			return result.Skills, result.NextCursor, nil
		})(yield)
	}
}

// DirectoryEntries returns an iterator that follows every page of a directory read.
func DirectoryEntries(ctx context.Context, session *mcp.ClientSession, params *ReadDirectoryParams) iter.Seq2[*mcp.Resource, error] {
	var initial ReadDirectoryParams
	if params != nil {
		initial = *params
		initial.Meta = maps.Clone(params.Meta)
	}
	return func(yield func(*mcp.Resource, error) bool) {
		request := initial
		allPages(initial.Cursor, func(cursor string) ([]*mcp.Resource, string, error) {
			request.Cursor = cursor
			result, err := ReadDirectory(ctx, session, &request)
			if err != nil {
				return nil, "", err
			}
			return result.Resources, result.NextCursor, nil
		})(yield)
	}
}

func allPages[T any](initialCursor string, fetch func(string) ([]T, string, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cursor := initialCursor
		seen := map[string]bool{}
		if cursor != "" {
			seen[cursor] = true
		}
		for {
			items, next, err := fetch(cursor)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, item := range items {
				if !yield(item, nil) {
					return
				}
			}
			if next == "" {
				return
			}
			if seen[next] {
				var zero T
				yield(zero, fmt.Errorf("skills: server repeated pagination cursor %q", next))
				return
			}
			seen[next] = true
			cursor = next
		}
	}
}

func requireCapability(session *mcp.ClientSession, directoryRead bool) error {
	if session == nil || session.InitializeResult() == nil || session.InitializeResult().Capabilities == nil {
		return fmt.Errorf("skills: session has no server capabilities")
	}
	settings, ok := session.InitializeResult().Capabilities.Extensions[ExtensionID]
	if !ok {
		return fmt.Errorf("skills: server does not advertise %s", ExtensionID)
	}
	if !directoryRead {
		return nil
	}
	m, ok := settings.(map[string]any)
	if !ok {
		return fmt.Errorf("skills: server advertised invalid extension settings")
	}
	enabled, _ := m["directoryRead"].(bool)
	if !enabled {
		return fmt.Errorf("skills: server does not advertise directoryRead")
	}
	return nil
}
