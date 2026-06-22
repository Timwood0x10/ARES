package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"goagentx/internal/tools/resources/core"
	"goagentx/internal/tools/resources/formatter"
)

// AgentToolConfig defines tool configuration for an agent.
type AgentToolConfig struct {
	// Enabled specifies which tools are enabled for the agent.
	// If empty, all tools are enabled.
	Enabled []string
	// Disabled specifies which tools are explicitly disabled.
	Disabled []string
	// Categories specifies which tool categories are allowed.
	// If empty, all categories are allowed.
	Categories []core.ToolCategory
}

// DefaultAgentToolConfig returns default tool configuration for an agent.
func DefaultAgentToolConfig() *AgentToolConfig {
	return &AgentToolConfig{
		Enabled:    nil, // All tools enabled
		Disabled:   nil, // No tools disabled
		Categories: nil, // All categories allowed
	}
}

// AgentTools manages tools for an agent instance.
type AgentTools struct {
	registry         *core.Registry
	config           *AgentToolConfig
	schemas          []core.ToolSchema
	capabilityEngine *core.CapabilityEngine
}

// NewAgentTools creates a new AgentTools instance with the given configuration.
func NewAgentTools(config *AgentToolConfig) *AgentTools {
	if config == nil {
		config = DefaultAgentToolConfig()
	}

	// Create filter
	filter := &core.ToolFilter{
		Enabled:    config.Enabled,
		Disabled:   config.Disabled,
		Categories: config.Categories,
	}

	// Apply filter to global registry
	filteredRegistry := core.GlobalRegistry.Filter(filter)

	// Create capability engine
	capEngine := core.NewCapabilityEngine(filteredRegistry)

	return &AgentTools{
		registry:         filteredRegistry,
		config:           config,
		schemas:          filteredRegistry.GetSchemas(),
		capabilityEngine: capEngine,
	}
}

// Execute executes a tool by name with logging and result formatting.
func (at *AgentTools) Execute(ctx context.Context, name string, params map[string]interface{}) (core.Result, error) {
	slog.Debug("Tool executing", "tool", name)

	startTime := time.Now()
	result, err := at.registry.Execute(ctx, name, params)
	duration := time.Since(startTime)

	if err != nil {
		slog.Error("Tool failed", "tool", name, "error", err, "duration", duration)
		return result, err
	}

	// Format result
	resultFormatter := formatter.NewResultFormatter()
	formattedResult := resultFormatter.Format(name, params, result, duration)

	if result.Metadata == nil {
		result.Metadata = make(map[string]interface{})
	}
	result.Metadata["formatted"] = formattedResult

	slog.Debug("Tool done", "tool", name, "duration", duration)

	return result, nil
}

// GetTool retrieves a tool by name.
func (at *AgentTools) GetTool(name string) (core.Tool, bool) {
	return at.registry.Get(name)
}

// ListTools returns all available tool names for this agent.
func (at *AgentTools) ListTools() []string {
	return at.registry.List()
}

// GetSchemas returns tool schemas for this agent.
func (at *AgentTools) GetSchemas() []core.ToolSchema {
	return at.schemas
}

// GetToolInfo returns information about a specific tool.
func (at *AgentTools) GetToolInfo(name string) map[string]interface{} {
	tool, exists := at.registry.Get(name)
	if !exists {
		return nil
	}

	return map[string]interface{}{
		"name":        tool.Name(),
		"description": tool.Description(),
		"category":    tool.Category(),
		"parameters":  tool.Parameters(),
	}
}

// GetCapabilityExport returns the tool capability export for this agent.
// This is useful for multi-agent coordination.
func (at *AgentTools) GetCapabilityExport(agentName string) *AgentCapabilityExport {
	tools := make([]string, len(at.schemas))
	for i, schema := range at.schemas {
		tools[i] = schema.Name
	}

	return &AgentCapabilityExport{
		AgentName:  agentName,
		Tools:      tools,
		Categories: at.getCategories(),
		ToolCount:  len(tools),
	}
}

// getCategories returns unique categories of enabled tools.
func (at *AgentTools) getCategories() []core.ToolCategory {
	categorySet := make(map[core.ToolCategory]bool)
	for _, schema := range at.schemas {
		categorySet[schema.Category] = true
	}

	categories := make([]core.ToolCategory, 0, len(categorySet))
	for category := range categorySet {
		categories = append(categories, category)
	}

	return categories
}

// GenerateToolPrompt generates a prompt string describing available tools.
// This can be injected into the agent's system prompt.
func (at *AgentTools) GenerateToolPrompt() string {
	if len(at.schemas) == 0 {
		return "No tools available."
	}

	prompt := "You have access to the following tools:\n\n"

	for _, schema := range at.schemas {
		prompt += fmt.Sprintf("- %s (%s): %s\n", schema.Name, schema.Category, schema.Description)
	}

	prompt += "\nUse these tools to accomplish tasks when appropriate."

	return prompt
}

// LogTools logs the loaded tools for debugging.
func (at *AgentTools) LogTools(agentName string) {
	slog.Info("Agent tools loaded",
		"agent", agentName,
		"tool_count", len(at.schemas),
		"tools", at.ListTools(),
		"categories", at.getCategories(),
	)
}

// MatchToolsByQuery returns tools that match the given query using capability detection.
// This reduces the number of tools presented to the LLM for better tool selection.
func (at *AgentTools) MatchToolsByQuery(query string) []core.Tool {
	return at.capabilityEngine.Match(query)
}

// MatchToolSchemasByQuery returns tool schemas that match the given query.
// This is useful for preparing tool definitions for LLM calls.
func (at *AgentTools) MatchToolSchemasByQuery(query string) []core.ToolSchema {
	tools := at.MatchToolsByQuery(query)
	schemas := make([]core.ToolSchema, len(tools))
	for i, tool := range tools {
		schemas[i] = core.ToolSchema{
			Name:        tool.Name(),
			Description: tool.Description(),
			Category:    tool.Category(),
			Parameters:  tool.Parameters(),
		}
	}
	return schemas
}

// DetectCapabilities returns capabilities detected from the query.
func (at *AgentTools) DetectCapabilities(query string) []core.Capability {
	return at.capabilityEngine.Detect(query)
}

// GetCapabilitySummary returns a summary of available capabilities and their tool counts.
func (at *AgentTools) GetCapabilitySummary() map[core.Capability]int {
	return at.capabilityEngine.GetCapabilitySummary()
}

// GetToolsByCapability returns tools that support a specific capability.
func (at *AgentTools) GetToolsByCapability(cap core.Capability) []core.Tool {
	return at.capabilityEngine.ToolsFor(cap)
}

// AgentCapabilityExport represents the tool capabilities of an agent.
// This is used for multi-agent coordination.
type AgentCapabilityExport struct {
	AgentName  string              `json:"agent_name"`
	Tools      []string            `json:"tools"`
	Categories []core.ToolCategory `json:"categories"`
	ToolCount  int                 `json:"tool_count"`
}

// String returns a string representation of the capability export.
func (ace *AgentCapabilityExport) String() string {
	return fmt.Sprintf("Agent %s has %d tools: %v", ace.AgentName, ace.ToolCount, ace.Tools)
}

// CreateAgentToolConfigs provides predefined tool configurations for common agent types.
var CreateAgentToolConfigs = struct {
	// Leader returns tool configuration for a leader agent (orchestration focused).
	Leader func() *AgentToolConfig
	// Worker returns tool configuration for a worker agent (task execution focused).
	Worker func() *AgentToolConfig
	// Research returns tool configuration for a research agent.
	Research func() *AgentToolConfig
	// All returns tool configuration with all tools enabled.
	All func() *AgentToolConfig
}{
	Leader: func() *AgentToolConfig {
		return &AgentToolConfig{
			Categories: []core.ToolCategory{
				core.CategoryCore,
				core.CategoryKnowledge,
				core.CategoryMemory,
			},
		}
	},
	Worker: func() *AgentToolConfig {
		return &AgentToolConfig{
			Categories: []core.ToolCategory{
				core.CategoryCore,
				core.CategoryData,
				core.CategorySystem,
			},
		}
	},
	Research: func() *AgentToolConfig {
		return &AgentToolConfig{
			Enabled: []string{
				"http_request",
				"knowledge_search",
				"text_processor",
				"json_tools",
			},
		}
	},
	All: func() *AgentToolConfig {
		return DefaultAgentToolConfig()
	},
}
