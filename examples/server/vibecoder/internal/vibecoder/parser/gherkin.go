package parser

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type GherkinFeature struct {
	Name      string
	Scenarios []GherkinScenario
}

type GherkinScenario struct {
	Name      string
	Steps     []string
	StepsHash string
	Line      int
}

func ParseGherkin(content []byte) (*GherkinFeature, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	feature := &GherkinFeature{}
	var currentScenario *GherkinScenario

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
			continue
		}

		if strings.HasPrefix(line, "Feature:") {
			feature.Name = strings.TrimSpace(strings.TrimPrefix(line, "Feature:"))
		} else if strings.HasPrefix(line, "Scenario:") {
			if currentScenario != nil {
				finalizeScenario(currentScenario)
				feature.Scenarios = append(feature.Scenarios, *currentScenario)
			}
			currentScenario = &GherkinScenario{
				Name: strings.TrimSpace(strings.TrimPrefix(line, "Scenario:")),
				Line: lineNum,
			}
		} else if isStep(line) {
			if currentScenario != nil {
				currentScenario.Steps = append(currentScenario.Steps, line)
			}
		}
	}
	if currentScenario != nil {
		finalizeScenario(currentScenario)
		feature.Scenarios = append(feature.Scenarios, *currentScenario)
	}

	return feature, nil
}

func isStep(line string) bool {
	words := strings.Fields(line)
	if len(words) == 0 {
		return false
	}
	kw := words[0]
	switch kw {
	case "Given", "When", "Then", "And", "But":
		return true
	}
	return false
}

func finalizeScenario(sc *GherkinScenario) {
	// Calculate hash of steps
	h := sha256.New()
	for _, s := range sc.Steps {
		h.Write([]byte(s))
	}
	sc.StepsHash = hex.EncodeToString(h.Sum(nil))[:8]
}
