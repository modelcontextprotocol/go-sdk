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
	memoryFilePath = flag.String("memory", "", "if set, persist the knowledge base to this file; otherwise, it will be stored in memory and lost on exit")
)

// HiArgs defines arguments for the greeting tool.
type HiArgs struct {
	Name string `json:"name"`
}

// Entity represents a knowledge graph node with observations.
type Entity struct {
	Name         string   `json:"name"`
	EntityType   string   `json:"entityType"`
	Observations []string `json:"observations"`
}

// Relation represents a directed edge between two entities.
type Relation struct {
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relationType"`
}

// Observation contains facts about an entity.
type Observation struct {
	EntityName string   `json:"entityName"`
	Contents   []string `json:"contents"`

	Observations []string `json:"observations,omitempty"` // Used for deletion operations
}

// KnowledgeGraph represents the complete graph structure.
type KnowledgeGraph struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}

// CreateEntitiesArgs defines the create entities tool parameters.
type CreateEntitiesArgs struct {
	Entities []Entity `json:"entities"`
}

// CreateEntitiesResult returns newly created entities.
type CreateEntitiesResult struct {
	Entities []Entity `json:"entities"`
}

// CreateRelationsArgs defines the create relations tool parameters.
type CreateRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

// CreateRelationsResult returns newly created relations.
type CreateRelationsResult struct {
	Relations []Relation `json:"relations"`
}

// AddObservationsArgs defines the add observations tool parameters.
type AddObservationsArgs struct {
	Observations []Observation `json:"observations"`
}

// AddObservationsResult returns newly added observations.
type AddObservationsResult struct {
	Observations []Observation `json:"observations"`
}

// DeleteEntitiesArgs defines the delete entities tool parameters.
type DeleteEntitiesArgs struct {
	EntityNames []string `json:"entityNames"`
}

// DeleteObservationsArgs defines the delete observations tool parameters.
type DeleteObservationsArgs struct {
	Deletions []Observation `json:"deletions"`
}

// DeleteRelationsArgs defines the delete relations tool parameters.
type DeleteRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

// SearchNodesArgs defines the search nodes tool parameters.
type SearchNodesArgs struct {
	Query string `json:"query"`
}

// OpenNodesArgs defines the open nodes tool parameters.
type OpenNodesArgs struct {
	Names []string `json:"names"`
}

func main() {
	flag.Parse()

	// Initialize storage backend
	var kbStore store
	kbStore = &memoryStore{}
	if *memoryFilePath != "" {
		kbStore = &fileStore{path: *memoryFilePath}
	}
	kb := knowledgeBase{s: kbStore}

	// Setup MCP server with knowledge base tools
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

	// Start server with appropriate transport
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
