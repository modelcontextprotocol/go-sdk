// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// Package skills implements the MCP Skills extension defined by SEP-2640.
package skills

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// ExtensionID is the capability identifier for the Skills extension.
	ExtensionID = "io.modelcontextprotocol/skills"
	// MethodList is the skills/list method name.
	MethodList = "skills/list"
	// MethodGet is the skills/get method name.
	MethodGet = "skills/get"
	// MethodReadDirectory is the resources/directory/read method name.
	MethodReadDirectory = "resources/directory/read"
)

// Frontmatter is the verbatim YAML frontmatter of a SKILL.md represented as JSON values.
type Frontmatter map[string]any

// Resource identifies and fingerprints one file in a skill.
type Resource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// Resources is either a complete static resource manifest or the dynamic marker.
type Resources struct {
	dynamic bool
	entries []*Resource
}

// StaticResources constructs a complete static resource manifest.
func StaticResources(resources ...*Resource) Resources {
	if resources == nil {
		resources = []*Resource{}
	}
	return Resources{entries: resources}
}

// DynamicResources constructs the marker used when stable digests cannot be published.
func DynamicResources() Resources { return Resources{dynamic: true} }

// IsDynamic reports whether r contains the dynamic marker.
func (r Resources) IsDynamic() bool { return r.dynamic }

// List returns the static manifest and true, or nil and false for dynamic or unset resources.
func (r Resources) List() ([]*Resource, bool) {
	if r.entries == nil || r.dynamic {
		return nil, false
	}
	return r.entries, true
}

func (r Resources) MarshalJSON() ([]byte, error) {
	if r.entries == nil && !r.dynamic {
		return nil, fmt.Errorf("skills: resources is not set")
	}
	if r.dynamic {
		return []byte(`"dynamic"`), nil
	}
	return json.Marshal(r.entries)
}

func (r *Resources) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte(`"dynamic"`)) {
		*r = DynamicResources()
		return nil
	}
	var entries []*Resource
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("skills: resources must be an array or %q: %w", "dynamic", err)
	}
	if entries == nil {
		return fmt.Errorf("skills: resources must not be null")
	}
	*r = StaticResources(entries...)
	return nil
}

// Skill is an entry returned by skills/list or skills/get.
type Skill struct {
	URI         string      `json:"uri"`
	Frontmatter Frontmatter `json:"frontmatter"`
	Resources   Resources   `json:"resources"`
}

// ListSkillsParams contains parameters for skills/list.
type ListSkillsParams struct {
	mcp.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

// ListSkillsResult is the result of skills/list.
type ListSkillsResult struct {
	mcp.ResultBase
	mcp.Cacheable
	ResultType string   `json:"resultType,omitempty"`
	NextCursor string   `json:"nextCursor,omitempty"`
	Skills     []*Skill `json:"skills"`
	omitCache  bool
}

func (r *ListSkillsResult) MarshalJSON() ([]byte, error) {
	type alias ListSkillsResult
	data, err := json.Marshal((*alias)(r))
	if err != nil || !r.omitCache {
		return data, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	delete(fields, "ttlMs")
	delete(fields, "cacheScope")
	return json.Marshal(fields)
}

// GetSkillParams contains parameters for skills/get.
type GetSkillParams struct {
	mcp.ParamsBase
	URI string `json:"uri"`
}

// GetSkillResult is the result of skills/get.
type GetSkillResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType,omitempty"`
	Skill      *Skill `json:"skill"`
}

// ReadDirectoryParams contains parameters for resources/directory/read.
type ReadDirectoryParams struct {
	mcp.ParamsBase
	URI    string `json:"uri"`
	Cursor string `json:"cursor,omitempty"`
}

// ReadDirectoryResult is the result of resources/directory/read.
type ReadDirectoryResult struct {
	mcp.ResultBase
	ResultType string          `json:"resultType,omitempty"`
	NextCursor string          `json:"nextCursor,omitempty"`
	Resources  []*mcp.Resource `json:"resources"`
}
