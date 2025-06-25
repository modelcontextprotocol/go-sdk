// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	httpAddr       = flag.String("http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")
	memoryFilePath = flag.String("memory", "", "If set, persist the knowledge base to this file; otherwise, it will be stored in memory and lost on exit.")
)

type HiArgs struct {
	Name string `json:"name"`
}

type Entity struct {
	Name         string   `json:"name"`
	EntityType   string   `json:"entityType"`
	Observations []string `json:"observations"`
}

type Relation struct {
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relationType"`
}

type Observation struct {
	EntityName string   `json:"entityName"`
	Contents   []string `json:"contents"`

	Observations []string `json:"observations,omitempty"` // For deletions.
}

type KnowledgeGraph struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}

type CreateEntitiesArgs struct {
	Entities []Entity `json:"entities"`
}

type CreateEntitiesResult struct {
	Entities []Entity `json:"entities"`
}

type CreateRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

type CreateRelationsResult struct {
	Relations []Relation `json:"relations"`
}

type AddObservationsArgs struct {
	Observations []Observation `json:"observations"`
}

type AddObservationsResult struct {
	Observations []Observation `json:"observations"`
}

type DeleteEntitiesArgs struct {
	EntityNames []string `json:"entityNames"`
}

type DeleteObservationsArgs struct {
	Deletions []Observation `json:"deletions"`
}

type DeleteRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

type SearchNodesArgs struct {
	Query string `json:"query"`
}

type OpenNodesArgs struct {
	Names []string `json:"names"`
}

func SayHi(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[HiArgs]) (*mcp.CallToolResultFor[struct{}], error) {
	return &mcp.CallToolResultFor[struct{}]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Hi " + params.Arguments.Name},
		},
	}, nil
}

func main() {
	flag.Parse()

	var kbStore store
	kbStore = &memoryStore{}
	if *memoryFilePath != "" {
		kbStore = &fileStore{path: *memoryFilePath}
	}
	kb := knowledgeBase{s: kbStore}

	server := mcp.NewServer("memory", "v0.0.1", nil)
	server.AddTools(mcp.NewServerTool("create_entities", "Create multiple new entities in the knowledge graph", kb.CreateEntities, mcp.Input(
		mcp.Property("entities", mcp.Description("Entities to create")),
	)))
	server.AddTools(mcp.NewServerTool("create_relations", "Create multiple new relations between entities", kb.CreateRelations, mcp.Input(
		mcp.Property("relations", mcp.Description("Relations to create")),
	)))
	server.AddTools(mcp.NewServerTool("add_observations", "Add new observations to existing entities", kb.AddObservations, mcp.Input(
		mcp.Property("observations", mcp.Description("Observations to add")),
	)))
	server.AddTools(mcp.NewServerTool("delete_entities", "Remove entities and their relations", kb.DeleteEntities, mcp.Input(
		mcp.Property("entityNames", mcp.Description("Names of entities to delete")),
	)))
	server.AddTools(mcp.NewServerTool("delete_observations", "Remove specific observations from entities", kb.DeleteObservations, mcp.Input(
		mcp.Property("deletions", mcp.Description("Observations to delete")),
	)))
	server.AddTools(mcp.NewServerTool("delete_relations", "Remove specific relations from the graph", kb.DeleteRelations, mcp.Input(
		mcp.Property("relations", mcp.Description("Relations to delete")),
	)))
	server.AddTools(mcp.NewServerTool("read_graph", "Read the entire knowledge graph", kb.ReadGraph))
	server.AddTools(mcp.NewServerTool("search_nodes", "Search for nodes based on query", kb.SearchNodes, mcp.Input(
		mcp.Property("query", mcp.Description("Query string")),
	)))
	server.AddTools(mcp.NewServerTool("open_nodes", "Retrieve specific nodes by name", kb.OpenNodes, mcp.Input(
		mcp.Property("names", mcp.Description("Names of nodes to open")),
	)))

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
			return server
		}, nil)
		log.Printf("MCP handler listening at %s", *httpAddr)
		http.ListenAndServe(*httpAddr, handler)
	} else {
		t := mcp.NewLoggingTransport(mcp.NewStdioTransport(), os.Stderr)
		if err := server.Run(context.Background(), t); err != nil {
			log.Printf("Server failed: %v", err)
		}
	}
}
