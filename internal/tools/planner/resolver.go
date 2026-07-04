package planner

import (
	"context"
	"fmt"
	"time"
)

// toolResolver implements ToolResolver by matching capability names
// against the existing tool registry using semantic name matching.
type toolResolver struct {
	provider ToolProvider
}

// ToolProvider abstracts access to the tool registry for resolution.
// This allows the planner to work with any registry implementation.
type ToolProvider interface {
	// ListTools returns all registered tool names.
	ListTools() []string

	// GetToolCapabilities returns the capabilities a tool provides.
	GetToolCapabilities(toolName string) ([]string, error)
}

// NewToolResolver creates a ToolResolver backed by the given ToolProvider.
func NewToolResolver(provider ToolProvider) ToolResolver {
	return &toolResolver{
		provider: provider,
	}
}

// capabilityMapping defines which tools provide which capabilities.
// This is the static mapping between capabilities and tools.
// In a future phase this will be replaced by tool self-declaration.
var capabilityMapping = map[string][]string{
	"Arithmetic":           {"calculator"},
	"Summation":            {"calculator"},
	"ExpressionEvaluation": {"calculator"},
	"Hashing":              {"hash_tool"},
	"Base64":               {"hash_tool"},
	"StringManipulation":   {"string_utils"},
	"Regex":                {"regex_tool"},
	"JSONProcessing":       {"json_tools"},
	"PDFParsing":           {"pdf_tool"},
	"TextExtraction":       {"pdf_tool"},
	"WebSearch":            {"web_search"},
	"HTTPRequest":          {"http_request"},
	"WebFetch":             {"web_search", "http_request"},
	"IDGeneration":         {"id_generator"},
	"CodeExecution":        {"code_runner"},
	"TaskPlanning":         {"task_planner"},
	"Embedding":            {"embedding"},
	"DateTime":             {"datetime"},
	"DataTransform":        {"data_transform"},
	"DataValidation":       {"data_validation"},
	"LogAnalysis":          {"log_analyzer"},
	"TextProcessor":        {"text_processor"},
}

// toolMetadata holds static scoring metadata for each tool.
var toolMetadata = map[string]struct {
	cost          int
	latency       time.Duration
	deterministic bool
	composable    bool
	sideEffects   bool
}{
	"calculator":      {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"hash_tool":       {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"string_utils":    {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"regex_tool":      {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"json_tools":      {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"pdf_tool":        {cost: 3, latency: 50 * time.Millisecond, deterministic: true, composable: true},
	"web_search":      {cost: 5, latency: 500 * time.Millisecond, deterministic: false, composable: true, sideEffects: false},
	"http_request":    {cost: 5, latency: 300 * time.Millisecond, deterministic: false, composable: true, sideEffects: true},
	"web_scraper":     {cost: 5, latency: 500 * time.Millisecond, deterministic: false, composable: true, sideEffects: false},
	"id_generator":    {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"code_runner":     {cost: 10, latency: 100 * time.Millisecond, deterministic: true, composable: true, sideEffects: true},
	"task_planner":    {cost: 3, latency: 10 * time.Millisecond, deterministic: false, composable: false},
	"embedding":       {cost: 8, latency: 100 * time.Millisecond, deterministic: true, composable: true, sideEffects: false},
	"datetime":        {cost: 1, latency: 1 * time.Millisecond, deterministic: true, composable: true},
	"data_transform":  {cost: 2, latency: 5 * time.Millisecond, deterministic: true, composable: true},
	"data_validation": {cost: 1, latency: 2 * time.Millisecond, deterministic: true, composable: true},
	"log_analyzer":    {cost: 3, latency: 10 * time.Millisecond, deterministic: true, composable: true},
	"text_processor":  {cost: 1, latency: 2 * time.Millisecond, deterministic: true, composable: true},
}

// Resolve finds all tools that can fulfill a capability requirement.
// Filters results to only include tools actually registered in the provider.
func (r *toolResolver) Resolve(_ context.Context, requirement *CapabilityRequirement) ([]ToolCandidate, error) {
	if requirement == nil {
		return nil, fmt.Errorf("planner: requirement is nil")
	}
	if requirement.Name == "" {
		return nil, fmt.Errorf("planner: requirement name is empty")
	}

	toolNames, exists := capabilityMapping[requirement.Name]
	if !exists || len(toolNames) == 0 {
		return nil, fmt.Errorf("planner: no tools found for capability %q", requirement.Name)
	}

	// Only include tools that are actually registered in the provider.
	registeredTools := make(map[string]bool)
	for _, name := range r.provider.ListTools() {
		registeredTools[name] = true
	}

	candidates := make([]ToolCandidate, 0, len(toolNames))
	for _, name := range toolNames {
		if !registeredTools[name] {
			continue
		}
		meta, ok := toolMetadata[name]
		if !ok {
			continue
		}

		candidates = append(candidates, ToolCandidate{
			ToolName:       name,
			CapabilityName: requirement.Name,
			Score:          0,
			Cost:           meta.cost,
			Latency:        meta.latency,
			Deterministic:  meta.deterministic,
			Composable:     meta.composable,
			SideEffects:    meta.sideEffects,
			SuccessRate:    0.95,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("planner: no registered tools for capability %q", requirement.Name)
	}

	return candidates, nil
}
