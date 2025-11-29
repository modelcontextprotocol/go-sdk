package domain

type NodeKind string

const (
	NodeKindCode            NodeKind = "Code"
	NodeKindRequirement     NodeKind = "Requirement"
	NodeKindFeature         NodeKind = "Feature"
	NodeKindTest            NodeKind = "Test"
	NodeKindGherkinFeature  NodeKind = "GherkinFeature"
	NodeKindGherkinScenario NodeKind = "GherkinScenario"
	NodeKindStepDefinition  NodeKind = "StepDefinition"
)

type EdgeType string

const (
	EdgeTypeDefines       EdgeType = "DEFINES"        // Requirement -> Feature
	EdgeTypeImplementedBy EdgeType = "IMPLEMENTED_BY" // Feature -> Code, Requirement -> Code
	EdgeTypeVerifies      EdgeType = "VERIFIES"       // Test/Scenario -> Requirement
	EdgeTypeExecutes      EdgeType = "EXECUTES"       // GherkinScenario -> StepDefinition
	EdgeTypeCalls         EdgeType = "CALLS"          // StepDefinition -> Code
	EdgeTypeDescribedBy   EdgeType = "DESCRIBED_BY"   // Requirement -> GherkinFeature
	EdgeTypeImports       EdgeType = "IMPORTS"        // Code -> Code (for architectural analysis)
)

type Node struct {
	ID         string                 `json:"id"`
	Kind       NodeKind               `json:"kind"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type Edge struct {
	SourceID string   `json:"source_id"`
	TargetID string   `json:"target_id"`
	Type     EdgeType `json:"type"`
}

type ViolationSeverity string

const (
	SeverityCritical ViolationSeverity = "CRITICAL"
	SeverityWarning  ViolationSeverity = "WARNING"
)

type ViolationKind string

const (
	ViolationKindArchLayer ViolationKind = "ARCH_LAYER_VIOLATION"
	ViolationKindBDDDrift  ViolationKind = "BDD_DRIFT"
)

type Violation struct {
	Severity ViolationSeverity `json:"severity"`
	Message  string            `json:"message"`
	File     string            `json:"file"`
	Kind     ViolationKind     `json:"kind"`
	Line     int               `json:"line,omitempty"` // Optional
}

// Helper structs for specific node properties (optional, for type safety if needed)
// For now, we rely on the generic Properties map for flexibility,
// but we can define structs to marshal/unmarshal specific kinds.

type RequirementProps struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	Status             string   `json:"status"`
	Priority           string   `json:"priority"`
	ExternalLink       string   `json:"externalLink"`
	AcceptanceCriteria []string `json:"acceptanceCriteria"`
}
