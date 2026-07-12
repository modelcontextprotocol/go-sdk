package mcp

import (
	"encoding/json"
	"testing"
)

func TestElicitParamsModeOmitempty(t *testing.T) {
	// When Mode is empty, it should be omitted from JSON serialization
	params := ElicitParams{
		Message: "Please provide your name",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("failed to marshal ElicitParams: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Mode should be omitted from the JSON output when empty
	if _, exists := m["mode"]; exists {
		t.Errorf("expected 'mode' to be omitted when empty, but got %q", m["mode"])
	}

	// Message should still be present
	if msg, ok := m["message"].(string); !ok || msg != "Please provide your name" {
		t.Errorf("expected message to be 'Please provide your name', got %v", m["message"])
	}
}

func TestElicitParamsModePresentWhenSet(t *testing.T) {
	// When Mode is set, it should appear in JSON serialization
	params := ElicitParams{
		Mode:    "form",
		Message: "Please provide your name",
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("failed to marshal ElicitParams: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Mode should be present in the JSON output
	if mode, ok := m["mode"].(string); !ok || mode != "form" {
		t.Errorf("expected mode to be 'form', got %v", m["mode"])
	}
}
