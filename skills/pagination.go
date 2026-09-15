// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"encoding/base64"
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func paginate[T any](items []T, cursor string, pageSize int, key func(T) string) ([]T, string, error) {
	if pageSize < 0 {
		return nil, "", fmt.Errorf("skills: invalid page size %d", pageSize)
	}
	if pageSize == 0 {
		pageSize = mcp.DefaultPageSize
	}
	cmp := func(a, b T) int { return strings.Compare(key(a), key(b)) }
	// An already-ordered catalog is the common case, and is the fast path:
	// paginating it costs one comparison pass and no allocation.
	if !slices.IsSortedFunc(items, cmp) {
		items = slices.Clone(items)
		slices.SortFunc(items, cmp)
	}
	for i, item := range items {
		uri := key(item)
		if uri == "" || i > 0 && uri == key(items[i-1]) {
			return nil, "", fmt.Errorf("skills: missing or duplicate pagination key %q", uri)
		}
	}
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(decoded) == 0 {
			return nil, "", invalidParams("invalid cursor")
		}
		// Resume at the first key after the cursor.
		last := string(decoded)
		var found bool
		start, found = slices.BinarySearchFunc(items, last, func(item T, last string) int {
			return strings.Compare(key(item), last)
		})
		if found {
			start++
		}
	}
	end := min(start+pageSize, len(items))
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
// It does not modify skills. A zero page size uses [mcp.DefaultPageSize].
func PaginateSkills(skills []*Skill, cursor string, pageSize int) ([]*Skill, string, error) {
	return paginate(skills, cursor, pageSize, func(skill *Skill) string {
		if skill == nil {
			return ""
		}
		return skill.URI
	})
}

// PaginateDirectoryResources returns one URI-ordered directory page without
// modifying resources. A zero page size uses [mcp.DefaultPageSize].
func PaginateDirectoryResources(resources []*mcp.Resource, cursor string, pageSize int) ([]*mcp.Resource, string, error) {
	return paginate(resources, cursor, pageSize, func(resource *mcp.Resource) string {
		if resource == nil {
			return ""
		}
		return resource.URI
	})
}

func allPages[T any](initialCursor string, fetch func(string) ([]T, string, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cursor := initialCursor
		// seen is populated only once a server hands out a second cursor,
		// so the common single-page walk allocates nothing.
		var seen map[string]bool
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
			if next == initialCursor || seen[next] {
				var zero T
				yield(zero, fmt.Errorf("skills: server repeated pagination cursor %q", next))
				return
			}
			if seen == nil {
				seen = map[string]bool{}
			}
			seen[next] = true
			cursor = next
		}
	}
}
