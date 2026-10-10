// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListResources returns a current, paginated listing of skill files. It reads
// frontmatter for resource metadata without hashing supporting files. Directory
// entries are available separately through ReadDirectory.
func (p *DirectoryProvider) ListResources(ctx context.Context, req *mcp.ListResourcesRequest) (*mcp.ListResourcesResult, error) {
	catalog, err := p.catalog(ctx, catalogMetadata)
	if err != nil {
		return nil, err
	}
	cursor := ""
	if req != nil && req.Params != nil {
		cursor = req.Params.Cursor
	}
	page, next, err := paginate(catalog.resources, cursor, p.pageSize, func(r *mcp.Resource) string { return r.URI })
	if err != nil {
		return nil, invalidParams(err.Error())
	}
	return &mcp.ListResourcesResult{
		Resources: page, NextCursor: next,
		Cacheable: mcp.Cacheable{CacheScope: "private"},
	}, nil
}

func (p *DirectoryProvider) listResourcesMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "resources/list" {
			return next(ctx, method, req)
		}
		byURI := make(map[string]*mcp.Resource)

		// Merge the underlying listing before paginating: its opaque cursors and
		// page boundaries need not match this provider's live URI-ordered catalog.
		request := *req.(*mcp.ListResourcesRequest)
		params := mcp.ListResourcesParams{}
		if request.Params != nil {
			params = *request.Params
			params.Meta = maps.Clone(params.Meta)
		}
		cursor := params.Cursor
		params.Cursor = ""
		request.Params = &params
		seen := make(map[string]bool)
		var result mcp.ListResourcesResult
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			response, err := next(ctx, method, &request)
			if err != nil {
				return nil, err
			}
			listed, ok := response.(*mcp.ListResourcesResult)
			if !ok || listed == nil {
				return nil, fmt.Errorf("skills: resources/list returned an invalid result")
			}
			if params.Cursor == "" {
				result = *listed
			}
			for _, resource := range listed.Resources {
				if resource == nil {
					return nil, fmt.Errorf("skills: resources/list returned a nil resource")
				}
				// Exact registrations also take precedence over templates on reads.
				byURI[resource.URI] = resource
			}
			if listed.NextCursor == "" {
				break
			}
			if seen[listed.NextCursor] {
				return nil, fmt.Errorf("skills: resources/list repeated a cursor")
			}
			seen[listed.NextCursor] = true
			params.Cursor = listed.NextCursor
		}
		catalog, err := p.catalog(ctx, catalogMetadata)
		if err != nil {
			return nil, err
		}
		for _, resource := range catalog.resources {
			if _, exists := byURI[resource.URI]; !exists {
				byURI[resource.URI] = resource
			}
		}
		result.Resources, result.NextCursor, err = PaginateDirectoryResources(slices.Collect(maps.Values(byURI)), cursor, p.pageSize)
		if err != nil {
			return nil, invalidParams(err.Error())
		}
		// Filesystem changes are discovered on request, without notifications.
		// Do not inherit a TTL intended only for the registered resources.
		result.Cacheable = mcp.Cacheable{CacheScope: "private"}
		return &result, nil
	}
}
