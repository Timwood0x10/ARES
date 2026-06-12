package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"goagentx/internal/tools/resources/core"
)

// TestDefaultAgentToolConfig tests default configuration.
func TestDefaultAgentToolConfig(t *testing.T) {
	config := DefaultAgentToolConfig()

	require.NotNil(t, config)

	if config.Enabled != nil {
		t.Error("Enabled should be nil for default config")
	}

	if config.Disabled != nil {
		t.Error("Disabled should be nil for default config")
	}

	if config.Categories != nil {
		t.Error("Categories should be nil for default config")
	}
}

// TestNewAgentTools tests creating AgentTools.
func TestNewAgentTools(t *testing.T) {
	tests := []struct {
		name   string
		config *AgentToolConfig
	}{
		{
			name:   "with nil config",
			config: nil,
		},
		{
			name:   "with default config",
			config: DefaultAgentToolConfig(),
		},
		{
			name: "with enabled tools",
			config: &AgentToolConfig{
				Enabled: []string{"tool1", "tool2"},
			},
		},
		{
			name: "with disabled tools",
			config: &AgentToolConfig{
				Disabled: []string{"tool1"},
			},
		},
		{
			name: "with categories",
			config: &AgentToolConfig{
				Categories: []core.ToolCategory{core.CategoryCore},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentTools := NewAgentTools(tt.config)

			require.NotNil(t, agentTools)

			if agentTools.registry == nil {
				t.Error("registry should not be nil")
			}

			if agentTools.config == nil {
				t.Error("config should not be nil")
			}

			if agentTools.capabilityEngine == nil {
				t.Error("capabilityEngine should not be nil")
			}
		})
	}
}

// TestAgentToolsExecute tests executing tools.
func TestAgentToolsExecute(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:        "test_agent_tool",
		description: "A test tool for agent",
		category:    core.CategoryCore,
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("test_agent_tool")
	}()

	agentTools := NewAgentTools(nil)

	ctx := context.Background()
	params := map[string]interface{}{
		"key": "value",
	}

	result, err := agentTools.Execute(ctx, "test_agent_tool", params)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !result.Success {
		t.Error("execute should return success")
	}

	// Test executing non-existing tool
	_, err = agentTools.Execute(ctx, "non_existing_tool", params)
	if err == nil {
		t.Error("expected error for non-existing tool")
	}
}

// TestAgentToolsGetTool tests retrieving tools.
func TestAgentToolsGetTool(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:        "get_test_tool",
		description: "A test tool for get",
		category:    core.CategoryCore,
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("get_test_tool")
	}()

	agentTools := NewAgentTools(nil)

	// Get existing tool
	tool, exists := agentTools.GetTool("get_test_tool")
	if !exists {
		t.Error("tool should exist")
	}
	if tool.Name() != "get_test_tool" {
		t.Errorf("tool name = %q, want %q", tool.Name(), "get_test_tool")
	}

	// Get non-existing tool
	_, exists = agentTools.GetTool("non_existing_tool")
	if exists {
		t.Error("tool should not exist")
	}
}

// TestAgentToolsListTools tests listing tools.
func TestAgentToolsListTools(t *testing.T) {
	// Register test tools
	tool1 := &mockTool{
		name:        "list_tool1",
		description: "First tool",
		category:    core.CategoryCore,
	}
	tool2 := &mockTool{
		name:        "list_tool2",
		description: "Second tool",
		category:    core.CategorySystem,
	}

	_ = core.Register(tool1)
	_ = core.Register(tool2)
	defer func() {
		_ = core.GlobalRegistry.Unregister("list_tool1")
		_ = core.GlobalRegistry.Unregister("list_tool2")
	}()

	agentTools := NewAgentTools(nil)

	tools := agentTools.ListTools()

	if len(tools) < 2 {
		t.Errorf("expected at least 2 tools, got %d", len(tools))
	}

	// Verify our test tools are in the list
	toolSet := make(map[string]bool)
	for _, name := range tools {
		toolSet[name] = true
	}

	if !toolSet["list_tool1"] {
		t.Error("list_tool1 should be in the list")
	}
	if !toolSet["list_tool2"] {
		t.Error("list_tool2 should be in the list")
	}
}

// TestAgentToolsGetSchemas tests getting tool schemas.
func TestAgentToolsGetSchemas(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:        "schema_test_tool",
		description: "A test tool for schema",
		category:    core.CategoryCore,
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("schema_test_tool")
	}()

	agentTools := NewAgentTools(nil)

	schemas := agentTools.GetSchemas()

	if len(schemas) == 0 {
		t.Error("schemas should not be empty")
	}

	// Find our test tool schema
	found := false
	for _, schema := range schemas {
		if schema.Name == "schema_test_tool" {
			found = true
			if schema.Description != "A test tool for schema" {
				t.Errorf("schema description = %q, want %q", schema.Description, "A test tool for schema")
			}
			if schema.Category != core.CategoryCore {
				t.Errorf("schema category = %q, want %q", schema.Category, core.CategoryCore)
			}
			break
		}
	}

	if !found {
		t.Error("schema for schema_test_tool not found")
	}
}

// TestAgentToolsGetToolInfo tests getting tool information.
func TestAgentToolsGetToolInfo(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:        "info_test_tool",
		description: "A test tool for info",
		category:    core.CategoryCore,
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("info_test_tool")
	}()

	agentTools := NewAgentTools(nil)

	// Get info for existing tool
	info := agentTools.GetToolInfo("info_test_tool")
	if info == nil {
		t.Error("info should not be nil for existing tool")
	}

	if info["name"] != "info_test_tool" {
		t.Errorf("info name = %v, want %v", info["name"], "info_test_tool")
	}

	if info["description"] != "A test tool for info" {
		t.Errorf("info description = %v, want %v", info["description"], "A test tool for info")
	}

	if info["category"] != core.CategoryCore {
		t.Errorf("info category = %v, want %v", info["category"], core.CategoryCore)
	}

	// Get info for non-existing tool
	info = agentTools.GetToolInfo("non_existing_tool")
	if info != nil {
		t.Error("info should be nil for non-existing tool")
	}
}

// TestAgentToolsGetCapabilityExport tests getting capability export.
func TestAgentToolsGetCapabilityExport(t *testing.T) {
	// Register test tools
	tool1 := &mockTool{
		name:        "export_tool1",
		description: "First tool",
		category:    core.CategoryCore,
	}
	tool2 := &mockTool{
		name:        "export_tool2",
		description: "Second tool",
		category:    core.CategorySystem,
	}

	_ = core.Register(tool1)
	_ = core.Register(tool2)
	defer func() {
		_ = core.GlobalRegistry.Unregister("export_tool1")
		_ = core.GlobalRegistry.Unregister("export_tool2")
	}()

	agentTools := NewAgentTools(nil)

	export := agentTools.GetCapabilityExport("test_agent")

	require.NotNil(t, export)

	if export.AgentName != "test_agent" {
		t.Errorf("export agent name = %q, want %q", export.AgentName, "test_agent")
	}

	if export.ToolCount == 0 {
		t.Error("export tool count should not be 0")
	}

	if len(export.Tools) == 0 {
		t.Error("export tools should not be empty")
	}

	if len(export.Categories) == 0 {
		t.Error("export categories should not be empty")
	}

	// Verify our test tools are in the export
	toolSet := make(map[string]bool)
	for _, name := range export.Tools {
		toolSet[name] = true
	}

	if !toolSet["export_tool1"] {
		t.Error("export_tool1 should be in export")
	}
	if !toolSet["export_tool2"] {
		t.Error("export_tool2 should be in export")
	}
}

// TestAgentCapabilityExportString tests String method.
func TestAgentCapabilityExportString(t *testing.T) {
	export := &AgentCapabilityExport{
		AgentName:  "test_agent",
		Tools:      []string{"tool1", "tool2"},
		Categories: []core.ToolCategory{core.CategoryCore},
		ToolCount:  2,
	}

	str := export.String()

	if str == "" {
		t.Error("String() should not return empty string")
	}

	// Verify it contains agent name
	if !contains(str, "test_agent") {
		t.Error("String() should contain agent name")
	}
}

// TestAgentToolsGenerateToolPrompt tests generating tool prompt.
func TestAgentToolsGenerateToolPrompt(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:        "prompt_test_tool",
		description: "A test tool for prompt",
		category:    core.CategoryCore,
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("prompt_test_tool")
	}()

	agentTools := NewAgentTools(nil)

	prompt := agentTools.GenerateToolPrompt()

	if prompt == "" {
		t.Error("prompt should not be empty")
	}

	// Verify it contains tool information
	if !contains(prompt, "prompt_test_tool") {
		t.Error("prompt should contain tool name")
	}

	if !contains(prompt, "A test tool for prompt") {
		t.Error("prompt should contain tool description")
	}
}

// TestAgentToolsMatchToolsByQuery tests matching tools by query.
func TestAgentToolsMatchToolsByQuery(t *testing.T) {
	// Register a test tool with math capability
	testTool := &mockTool{
		name:         "math_tool",
		description:  "A math tool",
		category:     core.CategoryCore,
		capabilities: []core.Capability{core.CapabilityMath},
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("math_tool")
	}()

	agentTools := NewAgentTools(nil)

	// Match with math query
	tools := agentTools.MatchToolsByQuery("calculate 5 + 3")

	// Should find the math tool
	found := false
	for _, tool := range tools {
		if tool.Name() == "math_tool" {
			found = true
			break
		}
	}

	if !found {
		t.Error("math_tool should be matched for math query")
	}

	// Match with non-matching query (result is ignored as it's just for testing)
	_ = agentTools.MatchToolsByQuery("random text without keywords")

	// May or may not find tools depending on keyword matching
	// This is acceptable behavior
}

// TestAgentToolsMatchToolSchemasByQuery tests matching tool schemas by query.
func TestAgentToolsMatchToolSchemasByQuery(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:         "schema_match_tool",
		description:  "A tool for schema matching",
		category:     core.CategoryCore,
		capabilities: []core.Capability{core.CapabilityText},
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("schema_match_tool")
	}()

	agentTools := NewAgentTools(nil)

	// Match with text query
	schemas := agentTools.MatchToolSchemasByQuery("parse text")

	// Should find the schema
	found := false
	for _, schema := range schemas {
		if schema.Name == "schema_match_tool" {
			found = true
			break
		}
	}

	if !found {
		t.Error("schema_match_tool should be matched for text query")
	}
}

// TestAgentToolsDetectCapabilities tests detecting capabilities.
func TestAgentToolsDetectCapabilities(t *testing.T) {
	agentTools := NewAgentTools(nil)

	tests := []struct {
		name     string
		query    string
		wantCaps []core.Capability
	}{
		{
			name:     "math query",
			query:    "calculate 5 + 3",
			wantCaps: []core.Capability{core.CapabilityMath},
		},
		{
			name:     "knowledge query",
			query:    "what is the capital of France",
			wantCaps: []core.Capability{core.CapabilityKnowledge},
		},
		{
			name:     "empty query",
			query:    "",
			wantCaps: []core.Capability{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := agentTools.DetectCapabilities(tt.query)

			if len(tt.wantCaps) == 0 {
				if len(caps) != 0 {
					t.Errorf("expected 0 capabilities, got %d", len(caps))
				}
			} else {
				if len(caps) == 0 {
					t.Error("expected at least one capability")
				}

				// Verify expected capabilities are detected
				capSet := make(map[core.Capability]bool)
				for _, cap := range caps {
					capSet[cap] = true
				}

				for _, expectedCap := range tt.wantCaps {
					if !capSet[expectedCap] {
						t.Errorf("capability %q not detected", expectedCap)
					}
				}
			}
		})
	}
}

// TestAgentToolsGetCapabilitySummary tests getting capability summary.
func TestAgentToolsGetCapabilitySummary(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:         "summary_tool",
		description:  "A tool for summary",
		category:     core.CategoryCore,
		capabilities: []core.Capability{core.CapabilityMath},
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("summary_tool")
	}()

	agentTools := NewAgentTools(nil)

	summary := agentTools.GetCapabilitySummary()

	if summary == nil {
		t.Error("summary should not be nil")
	}

	// Verify math capability is in summary
	if count, exists := summary[core.CapabilityMath]; !exists {
		t.Error("math capability should be in summary")
	} else if count == 0 {
		t.Error("math capability count should not be 0")
	}
}

// TestAgentToolsGetToolsByCapability tests getting tools by capability.
func TestAgentToolsGetToolsByCapability(t *testing.T) {
	// Register a test tool
	testTool := &mockTool{
		name:         "capability_tool",
		description:  "A tool for capability",
		category:     core.CategoryCore,
		capabilities: []core.Capability{core.CapabilityFile},
	}

	err := core.Register(testTool)
	if err != nil {
		t.Fatalf("failed to register test tool: %v", err)
	}
	defer func() {
		_ = core.GlobalRegistry.Unregister("capability_tool")
	}()

	agentTools := NewAgentTools(nil)

	// Get tools for file capability
	tools := agentTools.GetToolsByCapability(core.CapabilityFile)

	found := false
	for _, tool := range tools {
		if tool.Name() == "capability_tool" {
			found = true
			break
		}
	}

	if !found {
		t.Error("capability_tool should be found for file capability")
	}

	// Get tools for non-existing capability
	tools = agentTools.GetToolsByCapability(core.Capability("non_existing"))

	if len(tools) != 0 {
		t.Error("should return empty slice for non-existing capability")
	}
}

// TestCreateAgentToolConfigs tests predefined configurations.
func TestCreateAgentToolConfigs(t *testing.T) {
	// Test Leader config
	leaderConfig := CreateAgentToolConfigs.Leader()
	if leaderConfig != nil && len(leaderConfig.Categories) == 0 {
		t.Error("Leader config should have categories")
	}

	// Test Worker config
	workerConfig := CreateAgentToolConfigs.Worker()
	if workerConfig != nil && len(workerConfig.Categories) == 0 {
		t.Error("Worker config should have categories")
	}

	// Test Research config
	researchConfig := CreateAgentToolConfigs.Research()
	if researchConfig != nil && len(researchConfig.Enabled) == 0 {
		t.Error("Research config should have enabled tools")
	}

	// Test All config
	allConfig := CreateAgentToolConfigs.All()
	if allConfig == nil {
		t.Error("All config should not be nil")
	}
}

// TestAgentToolsWithFilter tests AgentTools with filtering.
func TestAgentToolsWithFilter(t *testing.T) {
	// Register test tools
	tool1 := &mockTool{
		name:        "filter_tool1",
		description: "First tool",
		category:    core.CategoryCore,
	}
	tool2 := &mockTool{
		name:        "filter_tool2",
		description: "Second tool",
		category:    core.CategorySystem,
	}

	_ = core.Register(tool1)
	_ = core.Register(tool2)
	defer func() {
		_ = core.GlobalRegistry.Unregister("filter_tool1")
		_ = core.GlobalRegistry.Unregister("filter_tool2")
	}()

	// Test with enabled filter
	config := &AgentToolConfig{
		Enabled: []string{"filter_tool1"},
	}

	agentTools := NewAgentTools(config)

	tools := agentTools.ListTools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool with enabled filter, got %d", len(tools))
	}

	if tools[0] != "filter_tool1" {
		t.Errorf("expected filter_tool1, got %s", tools[0])
	}

	// Test with disabled filter
	config = &AgentToolConfig{
		Disabled: []string{"filter_tool2"},
	}

	agentTools = NewAgentTools(config)

	tools = agentTools.ListTools()
	found := false
	for _, name := range tools {
		if name == "filter_tool2" {
			found = true
			break
		}
	}

	if found {
		t.Error("filter_tool2 should be disabled")
	}
}

// mockTool is a mock implementation of Tool interface for testing.
type mockTool struct {
	name         string
	description  string
	category     core.ToolCategory
	capabilities []core.Capability
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return m.description
}

func (m *mockTool) Category() core.ToolCategory {
	return m.category
}

func (m *mockTool) Capabilities() []core.Capability {
	if m.capabilities == nil {
		return []core.Capability{}
	}
	return m.capabilities
}

func (m *mockTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, map[string]interface{}{
		"executed": true,
		"tool":     m.name,
	}), nil
}

func (m *mockTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{
		Type:       "object",
		Properties: map[string]*core.Parameter{},
		Required:   []string{},
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
