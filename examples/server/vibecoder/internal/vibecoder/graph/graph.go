package graph

import (
	"sync"

	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/domain"
)

type Graph struct {
	mu           sync.RWMutex
	nodes        map[string]*domain.Node
	edges        map[string][]*domain.Edge // SourceID -> Edges
	reverseEdges map[string][]*domain.Edge // TargetID -> Edges
}

func NewGraph() *Graph {
	return &Graph{
		nodes:        make(map[string]*domain.Node),
		edges:        make(map[string][]*domain.Edge),
		reverseEdges: make(map[string][]*domain.Edge),
	}
}

func (g *Graph) AddNode(node *domain.Node) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[node.ID] = node
}

func (g *Graph) GetNode(id string) (*domain.Node, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

func (g *Graph) GetAllNodes() []*domain.Node {
	g.mu.RLock()
	defer g.mu.RUnlock()
	nodes := make([]*domain.Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		nodes = append(nodes, n)
	}
	return nodes
}

func (g *Graph) AddEdge(sourceID, targetID string, edgeType domain.EdgeType) {
	g.mu.Lock()
	defer g.mu.Unlock()

	edge := &domain.Edge{
		SourceID: sourceID,
		TargetID: targetID,
		Type:     edgeType,
	}

	// Avoid duplicates
	for _, e := range g.edges[sourceID] {
		if e.TargetID == targetID && e.Type == edgeType {
			return
		}
	}

	g.edges[sourceID] = append(g.edges[sourceID], edge)
	g.reverseEdges[targetID] = append(g.reverseEdges[targetID], edge)
}

func (g *Graph) GetEdgesFrom(sourceID string) []*domain.Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	// Return copy
	edges := g.edges[sourceID]
	result := make([]*domain.Edge, len(edges))
	copy(result, edges)
	return result
}

func (g *Graph) GetEdgesTo(targetID string) []*domain.Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	edges := g.reverseEdges[targetID]
	result := make([]*domain.Edge, len(edges))
	copy(result, edges)
	return result
}

// BlastRadius calculates impacted features and requirements given a code node ID.
// It traverses upwards (reverse edges) looking for Features and Requirements.
// The traversal follows: Code <- IMPLEMENTED_BY - Feature <- DEFINES - Requirement
// Or Code <- IMPLEMENTED_BY - Requirement
func (g *Graph) BlastRadius(codeID string) ([]string, []string) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	visited := make(map[string]bool)
	queue := []string{codeID}

	impactedFeatures := make(map[string]bool)
	impactedRequirements := make(map[string]bool)

	visited[codeID] = true

	for len(queue) > 0 {
		currentID := queue[0]
		queue = queue[1:]

		// Find what depends on currentID (reverse edges)
		// We care about relationships that imply "X depends on currentID"
		// If Feature IMPLEMENTED_BY Code, then Feature depends on Code.
		// So we traverse backwards along IMPLEMENTED_BY, DEFINES, etc.
		// Wait, IMPLEMENTED_BY is Feature -> Code.
		// So if I change Code, I look at who IMPLEMENTED_BY me. (The reverse edge of IMPLEMENTED_BY).

		for _, edge := range g.reverseEdges[currentID] {
			if !visited[edge.SourceID] {
				sourceNode, exists := g.nodes[edge.SourceID]
				if !exists {
					continue
				}

				if edge.Type == domain.EdgeTypeImplementedBy ||
					edge.Type == domain.EdgeTypeDefines ||
					edge.Type == domain.EdgeTypeCalls {

					visited[edge.SourceID] = true
					queue = append(queue, edge.SourceID)

					if sourceNode.Kind == domain.NodeKindFeature {
						impactedFeatures[sourceNode.ID] = true
					}
					if sourceNode.Kind == domain.NodeKindRequirement {
						impactedRequirements[sourceNode.ID] = true
					}
				}
			}
		}
	}

	features := make([]string, 0, len(impactedFeatures))
	for k := range impactedFeatures {
		features = append(features, k)
	}
	requirements := make([]string, 0, len(impactedRequirements))
	for k := range impactedRequirements {
		requirements = append(requirements, k)
	}

	return features, requirements
}

func (g *Graph) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes = make(map[string]*domain.Node)
	g.edges = make(map[string][]*domain.Edge)
	g.reverseEdges = make(map[string][]*domain.Edge)
}
