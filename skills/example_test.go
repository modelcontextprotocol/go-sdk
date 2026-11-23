// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills_test

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/skills"
)

func ExampleAddHandlers() {
	server := mcp.NewServer(&mcp.Implementation{Name: "skills", Version: "v1.0.0"}, nil)
	entry := &skills.Skill{
		URI: "skill://generated/SKILL.md",
		Frontmatter: skills.Frontmatter{
			"name": "generated", "description": "Instructions generated on demand.",
		},
		Resources: skills.DynamicResources(),
	}

	server.AddResource(&mcp.Resource{
		URI: entry.URI, Name: "generated", Description: "Instructions generated on demand.", MIMEType: "text/markdown",
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: entry.URI, MIMEType: "text/markdown",
			Text: "---\nname: generated\ndescription: Instructions generated on demand.\n---\n# Generated\n",
		}}}, nil
	})
	err := skills.AddHandlers(server, &skills.Handlers{
		List: func(context.Context, *mcp.ServerSession, *skills.ListSkillsParams) (*skills.ListSkillsResult, error) {
			return &skills.ListSkillsResult{Skills: []*skills.Skill{entry}}, nil
		},
		Get: func(_ context.Context, _ *mcp.ServerSession, params *skills.GetSkillParams) (*skills.GetSkillResult, error) {
			if params.URI != entry.URI {
				return nil, nil
			}
			return &skills.GetSkillResult{Skill: entry}, nil
		},
	}, nil)
	if err != nil {
		log.Fatal(err)
	}
}
