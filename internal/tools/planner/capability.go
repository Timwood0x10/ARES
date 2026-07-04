package planner

import (
	"context"
	"fmt"
)

// capabilityPlanner implements CapabilityPlanner with deterministic rules.
// It decomposes an Intent into an ordered list of CapabilityRequirements.
type capabilityPlanner struct{}

// NewCapabilityPlanner creates a deterministic capability planner.
func NewCapabilityPlanner() CapabilityPlanner {
	return &capabilityPlanner{}
}

// Plan decomposes the intent into capability requirements.
// For simple intents this is a 1:1 mapping.
// For complex intents it decomposes into multiple dependent requirements.
func (p *capabilityPlanner) Plan(_ context.Context, intent *Intent) ([]CapabilityRequirement, error) {
	if intent == nil {
		return nil, fmt.Errorf("planner: intent is nil")
	}
	if len(intent.RequiredCapabilities) == 0 {
		return nil, fmt.Errorf("planner: no capabilities required")
	}

	// Build requirements from the intent's capability list.
	requirements := make([]CapabilityRequirement, 0, len(intent.RequiredCapabilities))
	seen := make(map[string]bool)

	for _, capa := range intent.RequiredCapabilities {
		if seen[capa] {
			continue
		}
		seen[capa] = true

		req := CapabilityRequirement{
			Name:       capa,
			InputType:  inputTypeFor(capa),
			OutputType: outputTypeFor(capa),
			DependsOn:  dependenciesFor(capa),
		}
		requirements = append(requirements, req)
	}

	if len(requirements) == 0 {
		return nil, fmt.Errorf("planner: no capability requirements generated from intent")
	}

	return requirements, nil
}

// inputTypeFor returns the expected input type for a capability.
func inputTypeFor(capability string) string {
	switch capability {
	case "Arithmetic", "Summation":
		return "Expression"
	case "PDFParsing":
		return "File"
	case "Hashing", "Base64", "StringManipulation", "Regex":
		return "Text"
	case "WebSearch", "HTTPRequest", "WebFetch":
		return "URL"
	case "JSONProcessing":
		return "JSON"
	case "CodeExecution":
		return "Code"
	case "Embedding":
		return "Text"
	case "IDGeneration":
		return "None"
	case "TaskPlanning":
		return "Goal"
	default:
		return "Any"
	}
}

// outputTypeFor returns the output type for a capability.
func outputTypeFor(capability string) string {
	switch capability {
	case "Arithmetic", "Summation":
		return "Number"
	case "PDFParsing", "TextExtraction":
		return "Text"
	case "Hashing", "Base64", "StringManipulation", "Regex":
		return "Text"
	case "WebSearch", "WebFetch":
		return "JSON"
	case "HTTPRequest":
		return "Text"
	case "JSONProcessing":
		return "JSON"
	case "CodeExecution":
		return "Text"
	case "Embedding":
		return "Vector"
	case "IDGeneration":
		return "Identifier"
	case "TaskPlanning":
		return "Plan"
	default:
		return "Any"
	}
}

// dependenciesFor returns capability names that must precede this one.
func dependenciesFor(capability string) []string {
	switch capability {
	case "TextExtraction":
		return []string{"PDFParsing"}
	case "Embedding":
		return []string{"TextExtraction", "StringManipulation"}
	default:
		return nil
	}
}
