// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// This fixture exercises the generic Skills API without a filesystem provider.
// Run it against the three sep-2640-skills-* server conformance scenarios.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/skills"
)

func main() {
	addr := flag.String("http", "localhost:18299", "HTTP listen address")
	stateless := flag.Bool("stateless", true, "Use the modern stateless protocol")
	flag.Parse()
	server := mcp.NewServer(&mcp.Implementation{Name: "skills-conformance", Version: "v1"}, nil)
	files := map[string]string{
		"skill://demo/SKILL.md":            "---\nname: demo\ndescription: A demonstration skill.\nmetadata:\n  author: go-sdk\n---\n# Demo\nRead references/guide.md as needed.\n",
		"skill://demo/references/guide.md": "# Guide\nSupporting content.\n",
		"skill://demo/nested/SKILL.md":     "---\nname: nested\ndescription: A nested skill.\n---\n# Nested\n",
		"skill://other/SKILL.md":           "---\nname: other\ndescription: Another skill.\n---\n# Other\n",
	}
	var entries []*skills.Skill
	for _, item := range []struct{ uri, name, description string }{
		{"skill://demo/SKILL.md", "demo", "A demonstration skill."},
		{"skill://demo/nested/SKILL.md", "nested", "A nested skill."},
		{"skill://other/SKILL.md", "other", "Another skill."},
	} {
		var resources []*skills.Resource
		prefix := strings.TrimSuffix(item.uri, "SKILL.md")
		for uri, content := range files {
			if strings.HasPrefix(uri, prefix) {
				resources = append(resources, &skills.Resource{URI: uri, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content))), Size: int64(len(content))})
			}
		}
		entry := &skills.Skill{URI: item.uri, Frontmatter: skills.Frontmatter{"name": item.name, "description": item.description}, Resources: skills.StaticResources(resources...)}
		if item.name == "demo" {
			entry.Frontmatter["metadata"] = map[string]string{"author": "go-sdk"}
		}
		entries = append(entries, entry)
	}
	directories := map[string][]*mcp.Resource{"skill://demo/empty": {}}
	for uri, content := range files {
		resource := &mcp.Resource{URI: uri, Name: uri[strings.LastIndex(uri, "/")+1:], MIMEType: "text/markdown"}
		for _, entry := range entries {
			if entry.URI == uri {
				resource.Name = entry.Frontmatter["name"].(string)
				resource.Description = entry.Frontmatter["description"].(string)
			}
		}
		server.AddResource(resource, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: content}}}, nil
		})
		parent := uri[:strings.LastIndex(uri, "/")]
		directories[parent] = append(directories[parent], resource)
	}
	for _, uri := range []string{"skill://demo/references", "skill://demo/nested", "skill://demo/empty"} {
		directories["skill://demo"] = append(directories["skill://demo"], &mcp.Resource{URI: uri, Name: uri[strings.LastIndex(uri, "/")+1:], MIMEType: "inode/directory"})
	}
	if err := skills.AddHandlers(server, &skills.Handlers{
		List: func(_ context.Context, _ *mcp.ServerSession, p *skills.ListSkillsParams) (*skills.ListSkillsResult, error) {
			page, next, err := skills.PaginateSkills(entries, p.Cursor, 1)
			return &skills.ListSkillsResult{Skills: page, NextCursor: next}, err
		},
		Get: func(_ context.Context, _ *mcp.ServerSession, p *skills.GetSkillParams) (*skills.GetSkillResult, error) {
			for _, entry := range entries {
				if entry.URI == p.URI {
					return &skills.GetSkillResult{Skill: entry}, nil
				}
			}
			return nil, nil
		},
		ReadDirectory: func(_ context.Context, _ *mcp.ServerSession, p *skills.ReadDirectoryParams) (*skills.ReadDirectoryResult, error) {
			children, ok := directories[p.URI]
			if !ok {
				return nil, nil
			}
			page, next, err := skills.PaginateDirectoryResources(children, p.Cursor, 1)
			return &skills.ReadDirectoryResult{Resources: page, NextCursor: next}, err
		},
	}, nil); err != nil {
		log.Fatal(err)
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: *stateless})
	log.Fatal(http.ListenAndServe(*addr, handler))
}
