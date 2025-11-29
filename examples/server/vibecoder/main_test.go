package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/analysis"
	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/domain"
	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/graph"
)

func TestVibecoder(t *testing.T) {
	g := graph.NewGraph()
	an := analysis.NewAnalyzer(g)

	cwd, _ := os.Getwd()
	testRoot := filepath.Join(cwd, "testdata")

	// Manually scan
	err := filepath.Walk(testRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			content, _ := os.ReadFile(path)
			return an.AnalyzeFile(path, content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	an.IndexStepDefinitions()

	// Check Violation
	violations := an.FindViolations()
	found := false
	for _, v := range violations {
		if v.Kind == domain.ViolationKindArchLayer {
			// Check if it's the expected one
			if strings.Contains(v.Message, "Broken.ts") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("Expected architecture violation in Broken.ts")
	}

	// Check BDD Linking
	// Find scenario
	nodes := g.GetAllNodes()
	var scenNode *domain.Node
	for _, n := range nodes {
		if n.Kind == domain.NodeKindGherkinScenario {
			scenNode = n
			break
		}
	}
	if scenNode == nil {
		t.Fatal("Scenario not found")
	}

	// Check edge to StepDef
	edges := g.GetEdgesFrom(scenNode.ID)
	foundStep := false
	for _, e := range edges {
		if e.Type == domain.EdgeTypeExecutes {
			foundStep = true
			break
		}
	}

	if !foundStep {
		// print all step defs for debugging
		t.Log("Scenario edges:", len(edges))
		stepDefs := 0
		for _, n := range nodes {
			if n.Kind == domain.NodeKindStepDefinition {
				stepDefs++
				t.Logf("StepDef: %s Props: %v", n.ID, n.Properties)
			}
		}
		t.Logf("Total StepDefs: %d", stepDefs)
		t.Error("Expected Scenario to EXECUTE StepDefinition")
	}
}
