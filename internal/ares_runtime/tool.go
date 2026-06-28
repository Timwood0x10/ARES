package ares_runtime

import (
	"context"
	"log/slog"
	"sync"
)

// ToolPlugin records tool invocations via the ExecutionCollector and validates
// that tools used by steps are registered.
type ToolPlugin struct {
	mu        sync.Mutex
	name      string
	collector *ExecutionCollector
	registry  map[string]bool // registered tool names
}

// NewToolPlugin creates a ToolPlugin.
func NewToolPlugin(name string) *ToolPlugin {
	if name == "" {
		name = "tool"
	}
	return &ToolPlugin{
		name:     name,
		registry: make(map[string]bool),
	}
}

// WithCollector sets the execution collector for tool recording.
func (p *ToolPlugin) WithCollector(c *ExecutionCollector) *ToolPlugin {
	p.collector = c
	return p
}

// RegisterTool adds a tool name to the allowed registry.
func (p *ToolPlugin) RegisterTool(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.registry[name] = true
}

// IsRegistered returns true if the tool name is in the registry.
func (p *ToolPlugin) IsRegistered(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.registry[name]
}

// Name returns the plugin name.
func (p *ToolPlugin) Name() string { return p.name }

// Capabilities returns the capabilities.
func (p *ToolPlugin) Capabilities() []Capability { return []Capability{CapTool} }

// Start initializes the tool plugin.
func (p *ToolPlugin) Start(_ context.Context, _ EventBus) error { return nil }

// Stop shuts down the tool plugin.
func (p *ToolPlugin) Stop(_ context.Context) error { return nil }

// BeforeStep is a no-op for this plugin.
func (p *ToolPlugin) BeforeStep(_ context.Context, _ string, _ *Step) error { return nil }

// AfterStep inspects step metadata for tool invocation information and records
// tool calls via the collector.
func (p *ToolPlugin) AfterStep(_ context.Context, executionID string, result *StepResult) error {
	if result.Metadata == nil || p.collector == nil {
		return nil
	}
	if toolName, ok := result.Metadata[PayloadKeyToolName]; ok && toolName != "" {
		success := result.Status != StepStatusFailed
		output := result.Output
		if !success {
			output = result.Error
		}
		p.collector.RecordTool(result.StepID, toolName, "", output, result.Duration, success)
		slog.Debug("tool plugin: recorded tool call",
			"execution_id", executionID,
			"step_id", result.StepID,
			"tool", toolName,
		)
	}
	return nil
}

var _ WorkflowHook = (*ToolPlugin)(nil)
