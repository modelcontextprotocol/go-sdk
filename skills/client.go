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

// AddMethods registers the Skills extension methods that client may send.
// Call it before connecting the client to a server.
func AddMethods(client *mcp.Client) error {
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

// Client calls the Skills extension on a connected [mcp.ClientSession].
// Call [AddMethods] on the underlying [mcp.Client] before connecting.
// A Client may be used concurrently; do not modify its fields during use.
//
// Client does not prefetch content or cache entries. Keep entries scoped to
// their originating session and verify resource bytes before using them.
type Client struct {
	// Session is the connected MCP session. It must be non-nil.
	Session *mcp.ClientSession
	// Limits bounds each manifest returned by List, Get, or All.
	// The zero value imposes no caps. Use [BaselineLimits] to opt into
	// the spec's interoperability baseline.
	Limits Limits
}

// List calls skills/list and validates the response using c.Limits.
// If params is nil, List requests the first page.
func (c *Client) List(ctx context.Context, params *ListSkillsParams) (*ListSkillsResult, error) {
	if err := c.requireCapability(false); err != nil {
		return nil, err
	}
	limits := c.Limits
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if params == nil {
		params = &ListSkillsParams{}
	}
	request := *params
	request.Meta = maps.Clone(params.Meta)
	result, err := mcp.CallCustomMethod[*ListSkillsParams, *ListSkillsResult](ctx, c.Session, MethodList, &request)
	if err != nil {
		return nil, err
	}
	if err := validateListResult(result, limits); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid skills/list result: %w", err)
	}
	if err := c.validateEnvelope(result.ResultType, &result.Cacheable, result.cachePresent); err != nil {
		return nil, err
	}
	return result, nil
}

// Get calls skills/get and validates the response using c.Limits.
// The URI in params must identify a SKILL.md, whether or not it was listed.
func (c *Client) Get(ctx context.Context, params *GetSkillParams) (*GetSkillResult, error) {
	if err := c.requireCapability(false); err != nil {
		return nil, err
	}
	limits := c.Limits
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if params == nil || params.URI == "" {
		return nil, fmt.Errorf("skills: get requires a URI")
	}
	request := *params
	request.Meta = maps.Clone(params.Meta)
	result, err := mcp.CallCustomMethod[*GetSkillParams, *GetSkillResult](ctx, c.Session, MethodGet, &request)
	if err != nil {
		return nil, err
	}
	if err := validateGetResult(params.URI, result, limits); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid skill: %w", err)
	}
	if err := c.validateEnvelope(result.ResultType, &result.Cacheable, result.cachePresent); err != nil {
		return nil, err
	}
	return result, nil
}

// ReadDirectory calls resources/directory/read and validates the response.
// The server must advertise directoryRead, and params must specify a directory URI.
func (c *Client) ReadDirectory(ctx context.Context, params *ReadDirectoryParams) (*ReadDirectoryResult, error) {
	if err := c.requireCapability(true); err != nil {
		return nil, err
	}
	if params == nil || params.URI == "" {
		return nil, fmt.Errorf("skills: directory read requires a URI")
	}
	request := *params
	request.Meta = maps.Clone(params.Meta)
	result, err := mcp.CallCustomMethod[*ReadDirectoryParams, *ReadDirectoryResult](ctx, c.Session, MethodReadDirectory, &request)
	if err != nil {
		return nil, err
	}
	if err := ValidateDirectoryResult(params.URI, result); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid directory result: %w", err)
	}
	if err := c.validateEnvelope(result.ResultType, nil, false); err != nil {
		return nil, err
	}
	return result, nil
}

// All returns an iterator over skills/list, starting at params.Cursor.
// A nil params starts at the first page. Each page is validated as in [Client.List].
// The session, limits, and parameters are captured when All is called.
// The iterator stops after yielding its first error.
func (c *Client) All(ctx context.Context, params *ListSkillsParams) iter.Seq2[*Skill, error] {
	client := Client{}
	if c != nil {
		client = *c
	}
	var initial ListSkillsParams
	if params != nil {
		initial = *params
		initial.Meta = maps.Clone(params.Meta)
	}
	return func(yield func(*Skill, error) bool) {
		request := initial
		allPages(initial.Cursor, func(cursor string) ([]*Skill, string, error) {
			request.Cursor = cursor
			result, err := client.List(ctx, &request)
			if err != nil {
				return nil, "", err
			}
			return result.Skills, result.NextCursor, nil
		})(yield)
	}
}

// DirectoryEntries returns an iterator over a directory read, starting at params.Cursor.
// Each page is validated as in [Client.ReadDirectory].
// The iterator stops after yielding its first error.
func (c *Client) DirectoryEntries(ctx context.Context, params *ReadDirectoryParams) iter.Seq2[*mcp.Resource, error] {
	client := Client{}
	if c != nil {
		client = *c
	}
	var initial ReadDirectoryParams
	if params != nil {
		initial = *params
		initial.Meta = maps.Clone(params.Meta)
	}
	return func(yield func(*mcp.Resource, error) bool) {
		request := initial
		allPages(initial.Cursor, func(cursor string) ([]*mcp.Resource, string, error) {
			request.Cursor = cursor
			result, err := client.ReadDirectory(ctx, &request)
			if err != nil {
				return nil, "", err
			}
			return result.Resources, result.NextCursor, nil
		})(yield)
	}
}

func (c *Client) requireCapability(directoryRead bool) error {
	if c == nil {
		return fmt.Errorf("skills: nil client")
	}
	session := c.Session
	if session == nil || session.InitializeResult() == nil || session.InitializeResult().Capabilities == nil {
		return fmt.Errorf("skills: session has no server capabilities")
	}
	settings, ok := session.InitializeResult().Capabilities.Extensions[ExtensionID]
	if !ok {
		return fmt.Errorf("skills: server does not advertise %s", ExtensionID)
	}
	m, ok := settings.(map[string]any)
	if !ok {
		return fmt.Errorf("skills: server advertised invalid extension settings")
	}
	if session.InitializeResult().Capabilities.Resources == nil {
		return fmt.Errorf("skills: server does not advertise resources")
	}
	if !directoryRead {
		return nil
	}
	enabled, _ := m[capabilityDirectoryRead].(bool)
	if !enabled {
		return fmt.Errorf("skills: server does not advertise directoryRead")
	}
	return nil
}

func (c *Client) validateEnvelope(resultType string, cache *mcp.Cacheable, cachePresent bool) error {
	if c.Session.InitializeResult().ProtocolVersion < "2026-07-28" {
		return nil
	}
	if resultType != "complete" {
		return fmt.Errorf("skills: expected complete result, got %q", resultType)
	}
	if cache != nil {
		if !cachePresent {
			return fmt.Errorf("skills: missing ttlMs or cacheScope")
		}
		return validateCache(*cache)
	}
	return nil
}
