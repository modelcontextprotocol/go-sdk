// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package skills_test

import (
	"log"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/skills"
)

func ExampleAddFS() {
	server := mcp.NewServer(&mcp.Implementation{Name: "skills", Version: "v1.0.0"}, nil)
	skillFS := fstest.MapFS{
		"demo/SKILL.md": {
			Data: []byte("---\nname: demo\ndescription: Demonstrates an in-memory skill.\n---\n# Demo\n"),
		},
		"demo/references/guide.md": {Data: []byte("# Guide\n")},
	}
	if err := skills.AddFS(server, skillFS, &skills.DirectoryOptions{
		Cache: &skills.DirectoryCacheOptions{Preload: true},
	}); err != nil {
		log.Fatal(err)
	}
}
