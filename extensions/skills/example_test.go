// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/extensions/skills"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func ExampleAdd() {
	// !+skillsserver
	caps := &mcp.ServerCapabilities{}
	caps.AddExtension(skills.ExtensionID, nil)
	server := mcp.NewServer(&mcp.Implementation{Name: "skills", Version: "v1.0.0"}, &mcp.ServerOptions{Capabilities: caps})
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
	err := skills.Add(server, []*skills.Skill{entry}, nil)
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

	for skill, err := range skills.List(ctx, session, nil, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(skill.URI, skill.Frontmatter.Description())
	}
	// !-skillsclient

	// !+skillsverify
	result, err := skills.Get(ctx, session, &skills.GetSkillParams{URI: "skill://greeting/SKILL.md"})
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
