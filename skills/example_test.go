// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/skills"
)

func ExampleAddHandlers() {
	// !+skillsserver
	server := mcp.NewServer(&mcp.Implementation{Name: "skills", Version: "v1.0.0"}, nil)
	const uri = "skill://greeting/SKILL.md"
	const content = "---\nname: greeting\ndescription: Greet the user.\n---\n# Greeting\nSay hello to the user.\n"
	entry := &skills.Skill{
		URI: uri,
		Frontmatter: skills.Frontmatter{
			"name": "greeting", "description": "Greet the user.",
		},
		Resources: skills.StaticResources(&skills.Resource{
			URI: uri, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content))), Size: int64(len(content)),
		}),
	}

	server.AddResource(&mcp.Resource{
		URI: uri, Name: "greeting", Description: "Greet the user.", MIMEType: "text/markdown",
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: uri, MIMEType: "text/markdown", Text: content,
		}}}, nil
	})
	err := skills.AddHandlers(server, &skills.Handlers{
		List: func(_ context.Context, _ *mcp.ServerSession, params *skills.ListSkillsParams) (*skills.ListSkillsResult, error) {
			page, next, err := skills.PaginateSkills([]*skills.Skill{entry}, params.Cursor, 0)
			return &skills.ListSkillsResult{Skills: page, NextCursor: next}, err
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
	// !-skillsserver

	// !+skillsclient
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "skills-client", Version: "v1.0.0"}, nil)
	if err := skills.AddMethods(client); err != nil {
		log.Fatal(err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer serverSession.Close()
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	skillClient := &skills.Client{Session: session}
	for skill, err := range skillClient.All(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(skill.URI, skill.Frontmatter["description"])
	}
	// !-skillsclient

	// !+skillslimits
	limits := skills.DefaultLimits()
	limits.MaxTotalSize = 32 << 20
	skillClient = &skills.Client{Session: session, Limits: &limits}
	// !-skillslimits

	// !+skillsverify
	result, err := skillClient.Get(ctx, &skills.GetSkillParams{URI: "skill://greeting/SKILL.md"})
	if err != nil {
		log.Fatal(err)
	}
	resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: result.Skill.URI})
	if err != nil {
		log.Fatal(err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0] == nil || resource.Contents[0].URI != result.Skill.URI || resource.Contents[0].Blob != nil {
		log.Fatal("expected one text resource for SKILL.md")
	}
	if err := skills.VerifySkillMD(result.Skill, []byte(resource.Contents[0].Text)); err != nil {
		log.Fatal(err)
	}
	fmt.Println("verified", result.Skill.URI)
	// !-skillsverify

	// Output:
	// skill://greeting/SKILL.md Greet the user.
	// verified skill://greeting/SKILL.md
}
