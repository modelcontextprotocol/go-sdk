// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills_test

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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
	err := skills.AddHandlers(server, &skills.Handlers{
		List: func(context.Context, *mcp.ServerSession, *skills.ListSkillsParams) (*skills.ListSkillsResult, error) {
			return &skills.ListSkillsResult{Skills: []*skills.Skill{entry}}, nil
		},
		Get: func(_ context.Context, _ *mcp.ServerSession, params *skills.GetSkillParams) (*skills.GetSkillResult, error) {
			if params.URI != entry.URI {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "unknown skill"}
			}
			return &skills.GetSkillResult{Skill: entry}, nil
		},
	}, nil)
	if err != nil {
		log.Fatal(err)
	}
}
