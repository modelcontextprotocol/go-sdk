package analysis

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/domain"
	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/graph"
	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/parser"
)

type Analyzer struct {
	Graph *graph.Graph
}

func NewAnalyzer(g *graph.Graph) *Analyzer {
	return &Analyzer{Graph: g}
}

func (a *Analyzer) AnalyzeFile(path string, content []byte) error {
	// 1. Determine Layer/Type
	layer := detectLayer(path)

	// 2. Create/Update Node
	nodeID := path // Use path as ID for simplicity
	var node *domain.Node

	// Handle Gherkin
	if strings.HasSuffix(path, ".feature") {
		return a.analyzeGherkin(path, content)
	}

	// Handle Code
	lang := parser.DetectLanguage(path)
	if lang == parser.LangUnknown {
		// Just register generic file? Or skip.
		// Let's register generic code if inside source
		if layer != "" {
			node = &domain.Node{
				ID:   nodeID,
				Kind: domain.NodeKindCode,
				Metadata: map[string]interface{}{
					"layer":    layer,
					"language": "unknown",
				},
			}
			a.Graph.AddNode(node)
		}
		return nil
	}

	node = &domain.Node{
		ID:   nodeID,
		Kind: domain.NodeKindCode,
		Metadata: map[string]interface{}{
			"layer":    layer,
			"language": string(lang),
		},
	}
	a.Graph.AddNode(node)

	// 3. Parse Imports
	imports, err := parser.ParseImports(content, lang)
	if err == nil {
		for _, imp := range imports {
			// Resolve import path to ID (simplified)
			// Assuming import path is relative or absolute?
			// For now, we store the raw import path.
			// In a real system, we'd resolve this to the actual file ID.
			// Let's assume a simplified resolver or just store edge to "potential" ID.
			targetID := resolveImport(path, imp)
			a.Graph.AddEdge(nodeID, targetID, domain.EdgeTypeImports)
		}
	}

	// 4. Parse Step Definitions (if Test layer)
	if layer == "interface" || strings.Contains(path, "test") || strings.Contains(path, "steps") {
		steps, err := parser.ParseStepDefinitions(content, lang)
		if err == nil && len(steps) > 0 {
			for _, s := range steps {
				stepID := fmt.Sprintf("stepdef:%s:%s", s.FunctionName, s.Pattern)
				stepNode := &domain.Node{
					ID:   stepID,
					Kind: domain.NodeKindStepDefinition,
					Properties: map[string]interface{}{
						"regex_pattern": s.Pattern,
						"function_name": s.FunctionName,
						"filepath":      path,
						"line":          s.Line,
					},
				}
				a.Graph.AddNode(stepNode)
				a.Graph.AddEdge(stepID, nodeID, domain.EdgeTypeCalls)
			}
		}
	}

	return nil
}

func (a *Analyzer) analyzeGherkin(path string, content []byte) error {
	feat, err := parser.ParseGherkin(content)
	if err != nil {
		return err
	}

	// Create GherkinFeature Node
	featID := "gh:feat:" + strings.ReplaceAll(feat.Name, " ", "_")
	featNode := &domain.Node{
		ID:   featID,
		Kind: domain.NodeKindGherkinFeature,
		Properties: map[string]interface{}{
			"name": feat.Name,
			"file": path,
		},
	}
	a.Graph.AddNode(featNode)

	// Create Scenarios
	for _, sc := range feat.Scenarios {
		scID := "gh:scen:" + strings.ReplaceAll(sc.Name, " ", "_")
		scNode := &domain.Node{
			ID:   scID,
			Kind: domain.NodeKindGherkinScenario,
			Properties: map[string]interface{}{
				"name":       sc.Name,
				"file":       path,
				"steps_hash": sc.StepsHash,
				"line":       sc.Line,
				"steps":      sc.Steps,
			},
		}
		a.Graph.AddNode(scNode)
		// Link Feature -> Scenario (Conceptual containment, generic edge? or just naming convention)
		// Or assume implicit relationship. SRS doesn't define edge between Feat/Scen.
	}
	return nil
}

func detectLayer(path string) string {
	if strings.Contains(path, "/domain/") {
		return "domain"
	}
	if strings.Contains(path, "/application/") {
		return "application"
	}
	if strings.Contains(path, "/infrastructure/") {
		return "infrastructure"
	}
	if strings.Contains(path, "/interface/") || strings.Contains(path, "/api/") {
		return "interface"
	}
	return ""
}

// resolveImport attempts to map an import string to a file ID.
// This is very heuristic for the example.
func resolveImport(sourcePath, importStr string) string {
	// Remove quotes
	importStr = strings.Trim(importStr, "\"'")

	// If it starts with ., it's relative
	if strings.HasPrefix(importStr, ".") {
		dir := filepath.Dir(sourcePath)
		return filepath.Join(dir, importStr) // Simplified
	}
	// Else assume absolute or package alias?
	// For TS: src/domain/...
	// For this example, we'll return it as is, or prepend prefix if matches known patterns.
	return importStr
}

func (a *Analyzer) FindViolations() []domain.Violation {
	var violations []domain.Violation

	nodes := a.Graph.GetAllNodes()
	for _, node := range nodes {
		if node.Kind == domain.NodeKindCode {
			layer := node.Metadata["layer"]
			if layer == nil {
				continue
			}
			lStr := layer.(string)

			// Get imports
			edges := a.Graph.GetEdgesFrom(node.ID)
			for _, edge := range edges {
				if edge.Type == domain.EdgeTypeImports {
					target, ok := a.Graph.GetNode(edge.TargetID)
					// If we can't find the target node, we might try fuzzy matching or skip
					// For now skip if not found (external lib)
					if !ok {
						// Heuristic: check if targetID looks like infra/app
						if strings.Contains(edge.TargetID, "infrastructure") {
							// Check rules
							if lStr == "domain" {
								violations = append(violations, domain.Violation{
									Severity: domain.SeverityCritical,
									Message:  fmt.Sprintf("Domain Rule Broken: '%s' imports '%s' (Infrastructure).", node.ID, edge.TargetID),
									File:     node.ID,
									Kind:     domain.ViolationKindArchLayer,
								})
							}
						}
						continue
					}

					targetLayer := target.Metadata["layer"]
					if targetLayer == nil {
						continue
					}
					tlStr := targetLayer.(string)

					// Rule: Domain cannot import Infra or App
					if lStr == "domain" {
						if tlStr == "infrastructure" || tlStr == "application" {
							violations = append(violations, domain.Violation{
								Severity: domain.SeverityCritical,
								Message:  fmt.Sprintf("Domain Rule Broken: '%s' imports '%s' (%s).", node.ID, target.ID, tlStr),
								File:     node.ID,
								Kind:     domain.ViolationKindArchLayer,
							})
						}
					}
					// Rule: App cannot import Infra (strict) or should use ports.
					// SRS says: Alert if App imports Infra concrete.
					if lStr == "application" && tlStr == "infrastructure" {
						violations = append(violations, domain.Violation{
							Severity: domain.SeverityWarning,
							Message:  fmt.Sprintf("Application Alert: '%s' imports '%s' (Infrastructure). Should use Ports.", node.ID, target.ID),
							File:     node.ID,
							Kind:     domain.ViolationKindArchLayer,
						})
					}
				}
			}
		}
	}

	// BDD Drift Check
	scenarios := a.filterNodes(domain.NodeKindGherkinScenario)
	stepDefs := a.filterNodes(domain.NodeKindStepDefinition)

	for _, sc := range scenarios {
		scSteps, ok := sc.Properties["steps"].([]string)
		if !ok {
			continue
		}

		for _, stepText := range scSteps {
			cleanedStep := cleanStepText(stepText)
			matched := false
			for _, sd := range stepDefs {
				pattern, ok := sd.Properties["regex_pattern"].(string)
				if !ok {
					continue
				}
				if matchStep(cleanedStep, pattern) {
					matched = true
					break
				}
			}

			if !matched {
				violations = append(violations, domain.Violation{
					Severity: domain.SeverityWarning,
					Message:  fmt.Sprintf("BDD Drift/Missing: Step '%s' in '%s' has no matching StepDefinition.", stepText, sc.ID),
					File:     sc.Properties["file"].(string),
					Kind:     domain.ViolationKindBDDDrift,
					Line:     sc.Properties["line"].(int),
				})
			}
		}
	}

	return violations
}

// IndexStepDefinitions tries to link Scenarios to Steps
func (a *Analyzer) IndexStepDefinitions() {
	scenarios := a.filterNodes(domain.NodeKindGherkinScenario)
	stepDefs := a.filterNodes(domain.NodeKindStepDefinition)

	for _, sc := range scenarios {
		scSteps, ok := sc.Properties["steps"].([]string)
		if !ok {
			continue
		}

		for _, stepText := range scSteps {
			// Clean step text (remove Keyword)
			// "Given I have 5 items" -> "I have 5 items"
			cleanedStep := cleanStepText(stepText)

			for _, sd := range stepDefs {
				pattern, ok := sd.Properties["regex_pattern"].(string)
				if !ok {
					continue
				}

				// Simplified Regex matching
				// In real world, we'd use robust cucumber expression matching
				// Here we just try to see if it matches.
				// Pattern might be regex string.
				// Note: StepDef pattern often assumes full match.

				if matchStep(cleanedStep, pattern) {
					a.Graph.AddEdge(sc.ID, sd.ID, domain.EdgeTypeExecutes)
				}
			}
		}
	}
}

func cleanStepText(step string) string {
	parts := strings.Fields(step)
	if len(parts) > 1 {
		return strings.Join(parts[1:], " ")
	}
	return step
}

func matchStep(text, pattern string) bool {
	// Simple check: if pattern is regex
	re, err := regexp.Compile(pattern)
	if err == nil {
		return re.MatchString(text)
	}
	// Fallback to substring
	return strings.Contains(text, pattern)
}

func (a *Analyzer) filterNodes(kind domain.NodeKind) []*domain.Node {
	var res []*domain.Node
	for _, n := range a.Graph.GetAllNodes() {
		if n.Kind == kind {
			res = append(res, n)
		}
	}
	return res
}
