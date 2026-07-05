package planner

import (
	"fmt"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// RegistryProvider adapts a core.Registry to the ToolProvider interface
// by extracting capability declarations from each registered tool.
//
// Usage:
//
//	reg := core.NewRegistry()
//	provider := planner.NewRegistryProvider(reg)
//	resolver, err := planner.NewToolResolver(provider)
type RegistryProvider struct {
	reg *core.Registry
}

// NewRegistryProvider creates a ToolProvider that reads tools and their
// capabilities directly from a core.Registry.
func NewRegistryProvider(reg *core.Registry) *RegistryProvider {
	return &RegistryProvider{reg: reg}
}

// ListTools returns all registered tool names.
func (p *RegistryProvider) ListTools() []string {
	return p.reg.List()
}

// broadToGranular maps broad capability categories (as declared by built-in
// tools) to the granular capability names the planner's resolver uses.
// This enables dynamic discovery: tools declare "text" and the provider
// returns the specific capabilities the planner can match against.
var broadToGranular = map[string][]string{
	"math": {
		"Arithmetic", "Summation", "DiscreteMath",
		"Probability", "Statistics", "NumberTheory",
		"ExpressionEvaluation",
	},
	"text": {
		"StringManipulation", "Regex", "Hashing", "Base64",
		"JSONProcessing", CapabilityLogAnalysis, "TextProcessor",
		CapabilityDataTransform, CapabilityDataValidation,
	},
	"network": {
		"WebSearch", "HTTPRequest", "WebFetch",
	},
	"file": {
		"PDFParsing", "TextExtraction",
	},
	"time": {
		"DateTime",
	},
	"knowledge": {
		"WebSearch",
	},
	"external": {
		"CodeExecution", "IDGeneration",
	},
	"memory": {
		"Embedding",
	},
}

// GetToolCapabilities extracts capability names from the tool's Capabilities()
// method. Broad capability categories (e.g. "text", "math") are expanded into
// granular capability names (e.g. "Regex", "Arithmetic") that the planner's
// resolver can match against.
//
// Returns nil (no error) if the tool is not found — the resolver treats this
// as "no dynamic capabilities" and falls back to the static capability mapping.
func (p *RegistryProvider) GetToolCapabilities(toolName string) ([]string, error) {
	tool, exists := p.reg.Get(toolName)
	if !exists {
		return nil, fmt.Errorf("tool %q not found", toolName)
	}

	caps := tool.Capabilities()
	if len(caps) == 0 {
		return nil, nil
	}

	// Collect granular capabilities: each broad category expands to multiple
	// granular names that the planner's resolver understands.
	seen := make(map[string]bool)
	var names []string
	for _, c := range caps {
		broad := string(c)
		if granular, ok := broadToGranular[broad]; ok {
			for _, g := range granular {
				if !seen[g] {
					seen[g] = true
					names = append(names, g)
				}
			}
		} else if !seen[broad] {
			// Not a known broad category — pass through as-is.
			seen[broad] = true
			names = append(names, broad)
		}
	}
	return names, nil
}
