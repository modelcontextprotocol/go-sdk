// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func paginate[T any](items []T, cursor string, pageSize int, key func(T) string) ([]T, string, error) {
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) == 0 {
			return nil, "", fmt.Errorf("invalid cursor")
		}
		last := string(decoded)
		start = len(items)
		for i, item := range items {
			if key(item) > last {
				start = i
				break
			}
		}
	}
	end := start + min(pageSize, len(items)-start)
	page := slices.Clone(items[start:end])
	if page == nil {
		page = []T{}
	}
	if end == len(items) {
		return page, "", nil
	}
	next := base64.RawURLEncoding.EncodeToString([]byte(key(items[end-1])))
	return page, next, nil
}

// PaginateSkills returns one URI-ordered page and an opaque cursor for the next page.
// It does not modify skills.
func PaginateSkills(skills []*Skill, cursor string, pageSize int) ([]*Skill, string, error) {
	if pageSize < 0 {
		return nil, "", fmt.Errorf("skills: invalid page size %d", pageSize)
	}
	if pageSize == 0 {
		pageSize = mcp.DefaultPageSize
	}
	ordered := slices.Clone(skills)
	slices.SortFunc(ordered, func(a, b *Skill) int { return strings.Compare(a.URI, b.URI) })
	return paginate(ordered, cursor, pageSize, func(skill *Skill) string { return skill.URI })
}

// PaginateDirectoryResources returns one URI-ordered directory page and an
// opaque cursor for the next page. It does not modify resources.
func PaginateDirectoryResources(resources []*mcp.Resource, cursor string, pageSize int) ([]*mcp.Resource, string, error) {
	if pageSize < 0 {
		return nil, "", fmt.Errorf("skills: invalid page size %d", pageSize)
	}
	if pageSize == 0 {
		pageSize = mcp.DefaultPageSize
	}
	ordered := slices.Clone(resources)
	slices.SortFunc(ordered, func(a, b *mcp.Resource) int { return strings.Compare(a.URI, b.URI) })
	return paginate(ordered, cursor, pageSize, func(resource *mcp.Resource) string { return resource.URI })
}
