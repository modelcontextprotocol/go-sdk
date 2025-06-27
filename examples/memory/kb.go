// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// store is an interface for knowledge base persistence.
type store interface {
	Read() ([]byte, error)
	Write(data []byte) error
}

// memoryStore implements the store interface for in-memory persistence.
type memoryStore struct {
	data []byte
}

// Read reads data from the memory. If the data is empty, it returns an empty slice.
func (ms *memoryStore) Read() ([]byte, error) {
	return ms.data, nil
}

// Write writes data to the memory.
func (ms *memoryStore) Write(data []byte) error {
	ms.data = data
	return nil
}

// fileStore implements the store interface for file-based persistence.
type fileStore struct {
	path string
}

// Read reads data from the file. If the file does not exist, it returns an empty slice.
func (fs *fileStore) Read() ([]byte, error) {
	data, err := os.ReadFile(fs.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("failed to read file %s: %w", fs.path, err)
	}
	return data, nil
}

// Write writes data to the file.
func (fs *fileStore) Write(data []byte) error {
	if err := os.WriteFile(fs.path, data, 0600); err != nil {
		return fmt.Errorf("failed to write file %s: %w", fs.path, err)
	}
	return nil
}

type knowledgeBase struct {
	s store
}

type kbItem struct {
	Type string `json:"type"`

	// For Type == "entity"
	Name         string   `json:"name,omitempty"`
	EntityType   string   `json:"entityType,omitempty"`
	Observations []string `json:"observations,omitempty"`

	// For Type == "relation"
	From         string `json:"from,omitempty"`
	To           string `json:"to,omitempty"`
	RelationType string `json:"relationType,omitempty"`
}

func (k knowledgeBase) loadGraph() (KnowledgeGraph, error) {
	data, err := k.s.Read()
	if err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to read from store: %w", err)
	}

	if len(data) == 0 {
		return KnowledgeGraph{}, nil
	}

	var items []kbItem
	if err := json.Unmarshal(data, &items); err != nil {
		return KnowledgeGraph{}, fmt.Errorf("failed to unmarshal from store: %w", err)
	}

	graph := KnowledgeGraph{
		Entities:  []Entity{},
		Relations: []Relation{},
	}

	for _, item := range items {
		switch item.Type {
		case "entity":
			graph.Entities = append(graph.Entities, Entity{
				Name:         item.Name,
				EntityType:   item.EntityType,
				Observations: item.Observations,
			})
		case "relation":
			graph.Relations = append(graph.Relations, Relation{
				From:         item.From,
				To:           item.To,
				RelationType: item.RelationType,
			})
		}
	}

	return graph, nil
}

func (k knowledgeBase) saveGraph(graph KnowledgeGraph) error {
	items := make([]kbItem, 0, len(graph.Entities)+len(graph.Relations))

	for _, entity := range graph.Entities {
		items = append(items, kbItem{
			Type:         "entity",
			Name:         entity.Name,
			EntityType:   entity.EntityType,
			Observations: entity.Observations,
		})
	}

	for _, relation := range graph.Relations {
		items = append(items, kbItem{
			Type:         "relation",
			From:         relation.From,
			To:           relation.To,
			RelationType: relation.RelationType,
		})
	}

	itemsJSON, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("failed to marshal items: %w", err)
	}

	if err := k.s.Write(itemsJSON); err != nil {
		return fmt.Errorf("failed to write to store: %w", err)
	}
	return nil
}

func (k knowledgeBase) createEntities(entities []Entity) ([]Entity, error) {
	graph, err := k.loadGraph()
	if err != nil {
		return nil, err
	}

	var newEntities []Entity
	for _, entity := range entities {
		exists := false
		for _, existingEntity := range graph.Entities {
			if existingEntity.Name == entity.Name {
				exists = true
				break
			}
		}

		if !exists {
			newEntities = append(newEntities, entity)
			graph.Entities = append(graph.Entities, entity)
		}
	}

	if err := k.saveGraph(graph); err != nil {
		return nil, err
	}

	return newEntities, nil
}

func (k knowledgeBase) createRelations(relations []Relation) ([]Relation, error) {
	graph, err := k.loadGraph()
	if err != nil {
		return nil, err
	}

	var newRelations []Relation
	for _, relation := range relations {
		exists := false
		for _, existingRelation := range graph.Relations {
			if existingRelation.From == relation.From &&
				existingRelation.To == relation.To &&
				existingRelation.RelationType == relation.RelationType {
				exists = true
				break
			}
		}

		if !exists {
			newRelations = append(newRelations, relation)
			graph.Relations = append(graph.Relations, relation)
		}
	}

	if err := k.saveGraph(graph); err != nil {
		return nil, err
	}

	return newRelations, nil
}

func (k knowledgeBase) addObservations(observations []Observation) ([]Observation, error) {
	graph, err := k.loadGraph()
	if err != nil {
		return nil, err
	}

	var results []Observation

	for _, obs := range observations {
		entityIndex := -1
		for i, entity := range graph.Entities {
			if entity.Name == obs.EntityName {
				entityIndex = i
				break
			}
		}

		if entityIndex == -1 {
			return nil, fmt.Errorf("entity with name %s not found", obs.EntityName)
		}

		var newObservations []string
		for _, content := range obs.Contents {
			exists := slices.Contains(graph.Entities[entityIndex].Observations, content)

			if !exists {
				newObservations = append(newObservations, content)
				graph.Entities[entityIndex].Observations = append(graph.Entities[entityIndex].Observations, content)
			}
		}

		results = append(results, Observation{
			EntityName: obs.EntityName,
			Contents:   newObservations,
		})
	}

	if err := k.saveGraph(graph); err != nil {
		return nil, err
	}

	return results, nil
}

func (k knowledgeBase) deleteEntities(entityNames []string) error {
	graph, err := k.loadGraph()
	if err != nil {
		return err
	}

	// Create map for quick lookup
	entitiesToDelete := make(map[string]bool)
	for _, name := range entityNames {
		entitiesToDelete[name] = true
	}

	// Filter entities
	var filteredEntities []Entity
	for _, entity := range graph.Entities {
		if !entitiesToDelete[entity.Name] {
			filteredEntities = append(filteredEntities, entity)
		}
	}
	graph.Entities = filteredEntities

	// Filter relations
	var filteredRelations []Relation
	for _, relation := range graph.Relations {
		if !entitiesToDelete[relation.From] && !entitiesToDelete[relation.To] {
			filteredRelations = append(filteredRelations, relation)
		}
	}
	graph.Relations = filteredRelations

	return k.saveGraph(graph)
}

func (k knowledgeBase) deleteObservations(deletions []Observation) error {
	graph, err := k.loadGraph()
	if err != nil {
		return err
	}

	for _, deletion := range deletions {
		for i, entity := range graph.Entities {
			if entity.Name == deletion.EntityName {
				// Create a map for quick lookup
				observationsToDelete := make(map[string]bool)
				for _, observation := range deletion.Observations {
					observationsToDelete[observation] = true
				}

				// Filter observations
				var filteredObservations []string
				for _, observation := range entity.Observations {
					if !observationsToDelete[observation] {
						filteredObservations = append(filteredObservations, observation)
					}
				}

				graph.Entities[i].Observations = filteredObservations
				break
			}
		}
	}

	return k.saveGraph(graph)
}

func (k knowledgeBase) deleteRelations(relations []Relation) error {
	graph, err := k.loadGraph()
	if err != nil {
		return err
	}

	var filteredRelations []Relation
	for _, existingRelation := range graph.Relations {
		shouldKeep := true

		for _, relationToDelete := range relations {
			if existingRelation.From == relationToDelete.From &&
				existingRelation.To == relationToDelete.To &&
				existingRelation.RelationType == relationToDelete.RelationType {
				shouldKeep = false
				break
			}
		}

		if shouldKeep {
			filteredRelations = append(filteredRelations, existingRelation)
		}
	}

	graph.Relations = filteredRelations
	return k.saveGraph(graph)
}

func (k knowledgeBase) readGraph() (KnowledgeGraph, error) {
	return k.loadGraph()
}

func (k knowledgeBase) searchNodes(query string) (KnowledgeGraph, error) {
	graph, err := k.loadGraph()
	if err != nil {
		return KnowledgeGraph{}, err
	}

	queryLower := strings.ToLower(query)
	var filteredEntities []Entity

	// Filter entities
	for _, entity := range graph.Entities {
		if strings.Contains(strings.ToLower(entity.Name), queryLower) ||
			strings.Contains(strings.ToLower(entity.EntityType), queryLower) {
			filteredEntities = append(filteredEntities, entity)
			continue
		}

		// Check observations
		for _, observation := range entity.Observations {
			if strings.Contains(strings.ToLower(observation), queryLower) {
				filteredEntities = append(filteredEntities, entity)
				break
			}
		}
	}

	// Create map for quick entity lookup
	filteredEntityNames := make(map[string]bool)
	for _, entity := range filteredEntities {
		filteredEntityNames[entity.Name] = true
	}

	// Filter relations
	var filteredRelations []Relation
	for _, relation := range graph.Relations {
		if filteredEntityNames[relation.From] && filteredEntityNames[relation.To] {
			filteredRelations = append(filteredRelations, relation)
		}
	}

	return KnowledgeGraph{
		Entities:  filteredEntities,
		Relations: filteredRelations,
	}, nil
}

func (k knowledgeBase) openNodes(names []string) (KnowledgeGraph, error) {
	graph, err := k.loadGraph()
	if err != nil {
		return KnowledgeGraph{}, err
	}

	// Create map for quick name lookup
	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}

	// Filter entities
	var filteredEntities []Entity
	for _, entity := range graph.Entities {
		if nameSet[entity.Name] {
			filteredEntities = append(filteredEntities, entity)
		}
	}

	// Create map for quick entity lookup
	filteredEntityNames := make(map[string]bool)
	for _, entity := range filteredEntities {
		filteredEntityNames[entity.Name] = true
	}

	// Filter relations
	var filteredRelations []Relation
	for _, relation := range graph.Relations {
		if filteredEntityNames[relation.From] && filteredEntityNames[relation.To] {
			filteredRelations = append(filteredRelations, relation)
		}
	}

	return KnowledgeGraph{
		Entities:  filteredEntities,
		Relations: filteredRelations,
	}, nil
}

func (k knowledgeBase) CreateEntities(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[CreateEntitiesArgs]) (*mcp.CallToolResultFor[CreateEntitiesResult], error) {
	var res mcp.CallToolResultFor[CreateEntitiesResult]

	entities, err := k.createEntities(params.Arguments.Entities)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	// I think marshalling the entities and pass it as a content should not be necessary, but as for now, it looks like
	// the StructuredContent is not being unmarshalled in CallToolResultFor.
	entitiesJSON, err := json.Marshal(entities)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}
	res.Content = []mcp.Content{
		&mcp.TextContent{Text: string(entitiesJSON)},
	}

	res.StructuredContent = CreateEntitiesResult{
		Entities: entities,
	}

	return &res, nil
}

func (k knowledgeBase) CreateRelations(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[CreateRelationsArgs]) (*mcp.CallToolResultFor[CreateRelationsResult], error) {
	var res mcp.CallToolResultFor[CreateRelationsResult]

	relations, err := k.createRelations(params.Arguments.Relations)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	relationsJSON, err := json.Marshal(relations)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}
	res.Content = []mcp.Content{
		&mcp.TextContent{Text: string(relationsJSON)},
	}

	res.StructuredContent = CreateRelationsResult{
		Relations: relations,
	}

	return &res, nil
}

func (k knowledgeBase) AddObservations(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[AddObservationsArgs]) (*mcp.CallToolResultFor[AddObservationsResult], error) {
	var res mcp.CallToolResultFor[AddObservationsResult]

	observations, err := k.addObservations(params.Arguments.Observations)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	observationsJSON, err := json.Marshal(observations)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}
	res.Content = []mcp.Content{
		&mcp.TextContent{Text: string(observationsJSON)},
	}

	res.StructuredContent = AddObservationsResult{
		Observations: observations,
	}

	return &res, nil
}

func (k knowledgeBase) DeleteEntities(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[DeleteEntitiesArgs]) (*mcp.CallToolResultFor[struct{}], error) {
	var res mcp.CallToolResultFor[struct{}]

	err := k.deleteEntities(params.Arguments.EntityNames)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	res.Content = []mcp.Content{
		&mcp.TextContent{Text: "Entities deleted successfully"},
	}

	return &res, nil
}

func (k knowledgeBase) DeleteObservations(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[DeleteObservationsArgs]) (*mcp.CallToolResultFor[struct{}], error) {
	var res mcp.CallToolResultFor[struct{}]

	err := k.deleteObservations(params.Arguments.Deletions)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	res.Content = []mcp.Content{
		&mcp.TextContent{Text: "Observations deleted successfully"},
	}

	return &res, nil
}

func (k knowledgeBase) DeleteRelations(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[DeleteRelationsArgs]) (*mcp.CallToolResultFor[struct{}], error) {
	var res mcp.CallToolResultFor[struct{}]

	err := k.deleteRelations(params.Arguments.Relations)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	res.Content = []mcp.Content{
		&mcp.TextContent{Text: "Relations deleted successfully"},
	}

	return &res, nil
}

func (k knowledgeBase) ReadGraph(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[struct{}]) (*mcp.CallToolResultFor[KnowledgeGraph], error) {
	var res mcp.CallToolResultFor[KnowledgeGraph]

	graph, err := k.readGraph()
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	graphJSON, err := json.Marshal(graph)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}
	res.Content = []mcp.Content{
		&mcp.TextContent{Text: string(graphJSON)},
	}

	res.StructuredContent = graph
	return &res, nil
}

func (k knowledgeBase) SearchNodes(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[SearchNodesArgs]) (*mcp.CallToolResultFor[KnowledgeGraph], error) {
	var res mcp.CallToolResultFor[KnowledgeGraph]

	graph, err := k.readGraph()
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	graphJSON, err := json.Marshal(graph)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}
	res.Content = []mcp.Content{
		&mcp.TextContent{Text: string(graphJSON)},
	}

	res.StructuredContent = graph
	return &res, nil
}

func (k knowledgeBase) OpenNodes(ctx context.Context, ss *mcp.ServerSession, params *mcp.CallToolParamsFor[OpenNodesArgs]) (*mcp.CallToolResultFor[KnowledgeGraph], error) {
	var res mcp.CallToolResultFor[KnowledgeGraph]

	graph, err := k.openNodes(params.Arguments.Names)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}

	graphJSON, err := json.Marshal(graph)
	if err != nil {
		res.IsError = true
		res.Content = []mcp.Content{
			&mcp.TextContent{Text: err.Error()},
		}
		return &res, nil
	}
	res.Content = []mcp.Content{
		&mcp.TextContent{Text: string(graphJSON)},
	}

	res.StructuredContent = graph
	return &res, nil
}
