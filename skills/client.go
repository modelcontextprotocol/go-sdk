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

// ListOptions controls how list calls handle malformed entries.
type ListOptions struct {
	// SkipInvalidSkills omits invalid entries instead of rejecting the page.
	// Invalid response envelopes always fail. Skipped entries are reported in
	// ListSkillsResult.InvalidSkills and, if set, to OnInvalidSkill.
	SkipInvalidSkills bool
	// OnInvalidSkill receives each skipped entry, including during All.
	OnInvalidSkill func(InvalidSkill)
}

// List calls skills/list and validates the response without imposing manifest size caps.
// If params is nil, List requests the first page.
func List(ctx context.Context, session *mcp.ClientSession, params *ListSkillsParams, options ...ListOptions) (*ListSkillsResult, error) {
	if _, err := requireExtension(session); err != nil {
		return nil, err
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("skills: at most one ListOptions is allowed")
	}
	var opts ListOptions
	if len(options) == 1 {
		opts = options[0]
	}
	if params == nil {
		params = &ListSkillsParams{}
	}
	request := *params
	request.Meta = maps.Clone(params.Meta)
	result, err := mcp.CallCustomMethod[*ListSkillsParams, *ListSkillsResult](ctx, session, MethodList, &request)
	if err != nil {
		return nil, err
	}
	if err := validateEnvelope(session, result.ResultType, result.Cacheable, result.cachePresent); err != nil {
		return nil, err
	}
	if err := validateClientList(result, opts); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid skills/list result: %w", err)
	}
	return result, nil
}

// Get calls skills/get and validates the response without imposing manifest size caps.
// The URI in params must identify a SKILL.md, whether or not it was listed.
func Get(ctx context.Context, session *mcp.ClientSession, params *GetSkillParams) (*GetSkillResult, error) {
	if _, err := requireExtension(session); err != nil {
		return nil, err
	}
	limits := Limits{}
	if params == nil || params.URI == "" {
		return nil, fmt.Errorf("skills: get requires a URI")
	}
	request := *params
	request.Meta = maps.Clone(params.Meta)
	result, err := mcp.CallCustomMethod[*GetSkillParams, *GetSkillResult](ctx, session, MethodGet, &request)
	if err != nil {
		return nil, err
	}
	if err := validateGetResult(params.URI, result, limits); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid skill: %w", err)
	}
	if err := validateEnvelope(session, result.ResultType, result.Cacheable, result.cachePresent); err != nil {
		return nil, err
	}
	return result, nil
}

// ReadDirectory calls resources/directory/read and validates the response.
// The server must advertise directoryRead, and params must specify a directory URI.
func ReadDirectory(ctx context.Context, session *mcp.ClientSession, params *ReadDirectoryParams) (*ReadDirectoryResult, error) {
	if err := requireDirectoryRead(session); err != nil {
		return nil, err
	}
	if params == nil || params.URI == "" {
		return nil, fmt.Errorf("skills: directory read requires a URI")
	}
	request := *params
	request.Meta = maps.Clone(params.Meta)
	result, err := mcp.CallCustomMethod[*ReadDirectoryParams, *ReadDirectoryResult](ctx, session, MethodReadDirectory, &request)
	if err != nil {
		return nil, err
	}
	if err := ValidateDirectoryResult(params.URI, result); err != nil {
		return nil, fmt.Errorf("skills: server returned an invalid directory result: %w", err)
	}
	if err := validateResultType(session, result.ResultType); err != nil {
		return nil, err
	}
	return result, nil
}

// All returns an iterator over skills/list, starting at params.Cursor.
// A nil params starts at the first page. Each page is validated as in [List].
// The session, options, and parameters are captured when All is called.
// The iterator stops after yielding its first error.
func All(ctx context.Context, session *mcp.ClientSession, params *ListSkillsParams, options ...ListOptions) iter.Seq2[*Skill, error] {
	options = append([]ListOptions(nil), options...)
	var initial ListSkillsParams
	if params != nil {
		initial = *params
		initial.Meta = maps.Clone(params.Meta)
	}
	return func(yield func(*Skill, error) bool) {
		request := initial
		allPages(initial.Cursor, func(cursor string) ([]*Skill, string, error) {
			request.Cursor = cursor
			result, err := List(ctx, session, &request, options...)
			if err != nil {
				return nil, "", err
			}
			return result.Skills, result.NextCursor, nil
		})(yield)
	}
}

// DirectoryEntries returns an iterator over a directory read, starting at params.Cursor.
// Each page is validated as in [ReadDirectory].
// The iterator stops after yielding its first error.
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

// requireExtension reports the settings the server advertised for the Skills
// extension, or an error explaining which capability is missing.
func requireExtension(session *mcp.ClientSession) (map[string]any, error) {
	if session == nil {
		return nil, fmt.Errorf("skills: session has no server capabilities")
	}
	init := session.InitializeResult()
	if init == nil || init.Capabilities == nil {
		return nil, fmt.Errorf("skills: session has no server capabilities")
	}
	settings, ok := init.Capabilities.Extensions[ExtensionID]
	if !ok {
		return nil, fmt.Errorf("skills: server does not advertise %s", ExtensionID)
	}
	m, ok := settings.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("skills: server advertised invalid extension settings")
	}
	if init.Capabilities.Resources == nil {
		return nil, fmt.Errorf("skills: server does not advertise resources")
	}
	return m, nil
}

func requireDirectoryRead(session *mcp.ClientSession) error {
	settings, err := requireExtension(session)
	if err != nil {
		return err
	}
	if enabled, _ := settings[capabilityDirectoryRead].(bool); !enabled {
		return fmt.Errorf("skills: server does not advertise directoryRead")
	}
	return nil
}

// usesCaching reports whether the negotiated protocol version requires a result
// type, and with it the cache hints on skills/list and skills/get.
func usesCaching(session *mcp.ClientSession) bool {
	return session.InitializeResult().ProtocolVersion >= protocolVersionCaching
}

func validateResultType(session *mcp.ClientSession, resultType string) error {
	if usesCaching(session) && resultType != resultTypeComplete {
		return fmt.Errorf("skills: expected complete result, got %q", resultType)
	}
	return nil
}

func validateEnvelope(session *mcp.ClientSession, resultType string, cache mcp.Cacheable, cachePresent bool) error {
	if err := validateResultType(session, resultType); err != nil {
		return err
	}
	if !usesCaching(session) {
		return nil
	}
	if !cachePresent {
		return fmt.Errorf("skills: missing ttlMs or cacheScope")
	}
	return validateCache(cache)
}
