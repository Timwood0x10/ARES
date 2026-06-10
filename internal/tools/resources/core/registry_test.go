package core

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewRegistry tests creating a new Registry.
func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()

	require.NotNil(t, registry)

	if registry.tools == nil {
		t.Error("tools map should be initialized")
	}

	if len(registry.tools) != 0 {
		t.Errorf("new registry should have 0 tools, got %d", len(registry.tools))
	}
}

// TestRegistryRegister tests registering tools.
func TestRegistryRegister(t *testing.T) {
	tests := []struct {
		name    string
		tool    Tool
		wantErr bool
		errType error
	}{
		{
			name: "register valid tool",
			tool: &MockTool{
				name:        "test_tool",
				description: "A test tool",
				category:    CategoryCore,
			},
			wantErr: false,
		},
		{
			name:    "register nil tool",
			tool:    nil,
			wantErr: true,
			errType: ErrNilTool,
		},
		{
			name: "register duplicate tool",
			tool: &MockTool{
				name:        "duplicate_tool",
				description: "First registration",
				category:    CategoryCore,
			},
			wantErr: true,
			errType: ErrToolAlreadyRegistered,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewRegistry()

			// For duplicate test, register first
			if tt.name == "register duplicate tool" {
				firstTool := &MockTool{
					name:        "duplicate_tool",
					description: "First registration",
					category:    CategoryCore,
				}
				err := registry.Register(firstTool)
				if err != nil {
					t.Fatalf("first registration failed: %v", err)
				}
			}

			err := registry.Register(tt.tool)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				if tt.errType != nil && !errors.Is(err, tt.errType) {
					t.Errorf("expected error %v, got %v", tt.errType, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestRegistryRegisterDuplicate tests duplicate registration error.
func TestRegistryRegisterDuplicate(t *testing.T) {
	registry := NewRegistry()

	tool := &MockTool{
		name:        "duplicate_tool",
		description: "A tool",
		category:    CategoryCore,
	}

	// First registration should succeed
	err := registry.Register(tool)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Second registration should fail
	err = registry.Register(tool)
	if err == nil {
		t.Error("expected error for duplicate registration")
	}

	if !errors.Is(err, ErrToolAlreadyRegistered) {
		t.Errorf("expected ErrToolAlreadyRegistered, got %v", err)
	}
}

// TestRegistryGet tests retrieving tools.
func TestRegistryGet(t *testing.T) {
	registry := NewRegistry()

	tool := &MockTool{
		name:        "get_test_tool",
		description: "A tool for get test",
		category:    CategoryCore,
	}

	_ = registry.Register(tool)

	tests := []struct {
		name       string
		toolName   string
		wantExists bool
	}{
		{
			name:       "get existing tool",
			toolName:   "get_test_tool",
			wantExists: true,
		},
		{
			name:       "get non-existing tool",
			toolName:   "non_existing_tool",
			wantExists: false,
		},
		{
			name:       "get with empty name",
			toolName:   "",
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retrieved, exists := registry.Get(tt.toolName)

			if exists != tt.wantExists {
				t.Errorf("exists = %v, want %v", exists, tt.wantExists)
			}

			if tt.wantExists {
				if retrieved == nil {
					t.Error("retrieved tool should not be nil")
				}
				if retrieved.Name() != tt.toolName {
					t.Errorf("retrieved tool name = %q, want %q", retrieved.Name(), tt.toolName)
				}
			} else {
				if retrieved != nil {
					t.Error("retrieved tool should be nil for non-existing tool")
				}
			}
		})
	}
}

// TestRegistryUnregister tests removing tools.
func TestRegistryUnregister(t *testing.T) {
	registry := NewRegistry()

	tool := &MockTool{
		name:        "unregister_test_tool",
		description: "A tool for unregister test",
		category:    CategoryCore,
	}

	_ = registry.Register(tool)

	tests := []struct {
		name     string
		toolName string
		wantErr  bool
	}{
		{
			name:     "unregister existing tool",
			toolName: "unregister_test_tool",
			wantErr:  false,
		},
		{
			name:     "unregister non-existing tool",
			toolName: "non_existing_tool",
			wantErr:  true,
		},
		{
			name:     "unregister with empty name",
			toolName: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.Unregister(tt.toolName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}

				// Verify tool is removed
				_, exists := registry.Get(tt.toolName)
				if exists {
					t.Error("tool should not exist after unregister")
				}
			}
		})
	}
}

// TestRegistryList tests listing all tools.
func TestRegistryList(t *testing.T) {
	registry := NewRegistry()

	// Empty registry
	tools := registry.List()
	if len(tools) != 0 {
		t.Errorf("empty registry should return empty list, got %d tools", len(tools))
	}

	// Add tools
	tool1 := &MockTool{
		name:        "tool1",
		description: "First tool",
		category:    CategoryCore,
	}
	tool2 := &MockTool{
		name:        "tool2",
		description: "Second tool",
		category:    CategorySystem,
	}

	_ = registry.Register(tool1)
	_ = registry.Register(tool2)

	tools = registry.List()
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	// Verify tool names are present
	toolSet := make(map[string]bool)
	for _, name := range tools {
		toolSet[name] = true
	}

	if !toolSet["tool1"] {
		t.Error("tool1 should be in the list")
	}
	if !toolSet["tool2"] {
		t.Error("tool2 should be in the list")
	}
}

// TestRegistryCount tests counting tools.
func TestRegistryCount(t *testing.T) {
	registry := NewRegistry()

	if registry.Count() != 0 {
		t.Errorf("empty registry should have count 0, got %d", registry.Count())
	}

	tool := &MockTool{
		name:        "count_test_tool",
		description: "A tool for count test",
		category:    CategoryCore,
	}

	_ = registry.Register(tool)
	if registry.Count() != 1 {
		t.Errorf("registry should have count 1, got %d", registry.Count())
	}

	_ = registry.Unregister("count_test_tool")
	if registry.Count() != 0 {
		t.Errorf("registry should have count 0 after unregister, got %d", registry.Count())
	}
}

// TestRegistryExecute tests executing tools.
func TestRegistryExecute(t *testing.T) {
	registry := NewRegistry()

	tool := &MockTool{
		name:        "execute_test_tool",
		description: "A tool for execute test",
		category:    CategoryCore,
	}

	_ = registry.Register(tool)

	ctx := context.Background()
	params := map[string]interface{}{
		"key": "value",
	}

	result, err := registry.Execute(ctx, "execute_test_tool", params)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !result.Success {
		t.Error("execute should return success")
	}

	// Test executing non-existing tool
	_, err = registry.Execute(ctx, "non_existing_tool", params)
	if err == nil {
		t.Error("expected error for non-existing tool")
	}
}

// TestRegistryClear tests clearing all tools.
func TestRegistryClear(t *testing.T) {
	registry := NewRegistry()

	// Add multiple tools
	for i := 0; i < 5; i++ {
		tool := &MockTool{
			name:        "tool_" + string(rune(i)),
			description: "Tool",
			category:    CategoryCore,
		}
		_ = registry.Register(tool)
	}

	if registry.Count() != 5 {
		t.Errorf("expected 5 tools, got %d", registry.Count())
	}

	registry.Clear()

	if registry.Count() != 0 {
		t.Errorf("registry should be empty after clear, got %d tools", registry.Count())
	}

	tools := registry.List()
	if len(tools) != 0 {
		t.Errorf("list should be empty after clear, got %d tools", len(tools))
	}
}

// TestRegistryFilter tests filtering tools.
func TestRegistryFilter(t *testing.T) {
	registry := NewRegistry()

	// Register tools with different categories
	tool1 := &MockTool{
		name:        "core_tool",
		description: "Core tool",
		category:    CategoryCore,
	}
	tool2 := &MockTool{
		name:        "system_tool",
		description: "System tool",
		category:    CategorySystem,
	}
	tool3 := &MockTool{
		name:        "data_tool",
		description: "Data tool",
		category:    CategoryData,
	}

	_ = registry.Register(tool1)
	_ = registry.Register(tool2)
	_ = registry.Register(tool3)

	tests := []struct {
		name      string
		filter    *ToolFilter
		wantCount int
	}{
		{
			name: "filter by enabled list",
			filter: &ToolFilter{
				Enabled: []string{"core_tool", "system_tool"},
			},
			wantCount: 2,
		},
		{
			name: "filter by disabled list",
			filter: &ToolFilter{
				Disabled: []string{"data_tool"},
			},
			wantCount: 2,
		},
		{
			name: "filter by category",
			filter: &ToolFilter{
				Categories: []ToolCategory{CategoryCore},
			},
			wantCount: 1,
		},
		{
			name:      "filter with empty filter",
			filter:    &ToolFilter{},
			wantCount: 3,
		},
		{
			name:      "filter with nil filter",
			filter:    nil,
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := registry.Filter(tt.filter)

			if filtered == nil {
				t.Fatal("filtered registry should not be nil")
			}

			if filtered.Count() != tt.wantCount {
				t.Errorf("filtered count = %d, want %d", filtered.Count(), tt.wantCount)
			}
		})
	}
}

// TestRegistryFilterByCategory tests filtering by category.
func TestRegistryFilterByCategory(t *testing.T) {
	registry := NewRegistry()

	// Register tools with different categories
	tool1 := &MockTool{
		name:        "core_tool",
		description: "Core tool",
		category:    CategoryCore,
	}
	tool2 := &MockTool{
		name:        "system_tool",
		description: "System tool",
		category:    CategorySystem,
	}
	tool3 := &MockTool{
		name:        "another_core_tool",
		description: "Another core tool",
		category:    CategoryCore,
	}

	_ = registry.Register(tool1)
	_ = registry.Register(tool2)
	_ = registry.Register(tool3)

	filtered := registry.FilterByCategory(CategoryCore)

	if filtered.Count() != 2 {
		t.Errorf("expected 2 core tools, got %d", filtered.Count())
	}

	// Verify all filtered tools are core category
	for _, name := range filtered.List() {
		tool, _ := filtered.Get(name)
		if tool.Category() != CategoryCore {
			t.Errorf("tool %s has category %s, want %s", name, tool.Category(), CategoryCore)
		}
	}
}

// TestRegistryGetSchemas tests getting tool schemas.
func TestRegistryGetSchemas(t *testing.T) {
	registry := NewRegistry()

	// Empty registry
	schemas := registry.GetSchemas()
	if len(schemas) != 0 {
		t.Errorf("empty registry should return empty schemas, got %d", len(schemas))
	}

	// Add tools
	tool1 := &MockTool{
		name:        "schema_tool1",
		description: "First tool",
		category:    CategoryCore,
	}
	tool2 := &MockTool{
		name:        "schema_tool2",
		description: "Second tool",
		category:    CategorySystem,
	}

	_ = registry.Register(tool1)
	_ = registry.Register(tool2)

	schemas = registry.GetSchemas()
	if len(schemas) != 2 {
		t.Errorf("expected 2 schemas, got %d", len(schemas))
	}

	// Verify schema content
	schemaMap := make(map[string]ToolSchema)
	for _, schema := range schemas {
		schemaMap[schema.Name] = schema
	}

	if schema, exists := schemaMap["schema_tool1"]; !exists {
		t.Error("schema for schema_tool1 should exist")
	} else {
		if schema.Description != "First tool" {
			t.Errorf("schema description = %q, want %q", schema.Description, "First tool")
		}
		if schema.Category != CategoryCore {
			t.Errorf("schema category = %q, want %q", schema.Category, CategoryCore)
		}
	}
}

// TestRegistryConcurrency tests concurrent access to registry.
func TestRegistryConcurrency(t *testing.T) {
	registry := NewRegistry()

	// Register initial tool
	tool := &MockTool{
		name:        "concurrent_tool",
		description: "A tool for concurrency test",
		category:    CategoryCore,
	}
	_ = registry.Register(tool)

	var wg sync.WaitGroup
	numGoroutines := 10

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = registry.Get("concurrent_tool")
			_ = registry.List()
			_ = registry.Count()
		}()
	}

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tool := &MockTool{
				name:        "concurrent_tool_" + string(rune(id)),
				description: "Concurrent tool",
				category:    CategoryCore,
			}
			_ = registry.Register(tool)
		}(i)
	}

	wg.Wait()

	// Verify registry is still functional
	if registry.Count() < 1 {
		t.Error("registry should have at least 1 tool after concurrent operations")
	}
}

// TestToolGroup tests ToolGroup functionality.
func TestToolGroup(t *testing.T) {
	group := NewToolGroup("test_group", "A test group")

	if group.Name() != "test_group" {
		t.Errorf("group name = %q, want %q", group.Name(), "test_group")
	}

	if group.Description() != "A test group" {
		t.Errorf("group description = %q, want %q", group.Description(), "A test group")
	}

	// Register tool in group
	tool := &MockTool{
		name:        "group_tool",
		description: "A tool in group",
		category:    CategoryCore,
	}

	err := group.Register(tool)
	if err != nil {
		t.Fatalf("failed to register tool in group: %v", err)
	}

	// Get tool from group
	retrieved, exists := group.Get("group_tool")
	if !exists {
		t.Error("tool should exist in group")
	}
	if retrieved.Name() != "group_tool" {
		t.Errorf("retrieved tool name = %q, want %q", retrieved.Name(), "group_tool")
	}

	// List tools in group
	tools := group.List()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool in group, got %d", len(tools))
	}
}

// TestGlobalRegistry tests global registry functions.
func TestGlobalRegistry(t *testing.T) {
	// Save original state
	originalCount := GlobalRegistry.Count()

	// Register tool
	tool := &MockTool{
		name:        "global_test_tool",
		description: "A tool for global registry test",
		category:    CategoryCore,
	}

	err := Register(tool)
	if err != nil {
		t.Fatalf("failed to register tool in global registry: %v", err)
	}

	// Get tool
	retrieved, exists := Get("global_test_tool")
	if !exists {
		t.Error("tool should exist in global registry")
	}
	if retrieved.Name() != "global_test_tool" {
		t.Errorf("retrieved tool name = %q, want %q", retrieved.Name(), "global_test_tool")
	}

	// List tools
	tools := List()
	found := false
	for _, name := range tools {
		if name == "global_test_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("global_test_tool should be in global registry list")
	}

	// Execute tool
	ctx := context.Background()
	result, err := Execute(ctx, "global_test_tool", map[string]interface{}{})
	if err != nil {
		t.Fatalf("failed to execute tool: %v", err)
	}
	if !result.Success {
		t.Error("execute should return success")
	}

	// Cleanup
	_ = GlobalRegistry.Unregister("global_test_tool")

	// Verify cleanup
	if GlobalRegistry.Count() != originalCount {
		t.Errorf("global registry count should be restored to %d, got %d", originalCount, GlobalRegistry.Count())
	}
}

// TestRegistryErrors tests error constants.
func TestRegistryErrors(t *testing.T) {
	if ErrNilTool == nil {
		t.Error("ErrNilTool should not be nil")
	}

	if ErrToolNotFound == nil {
		t.Error("ErrToolNotFound should not be nil")
	}

	if ErrToolAlreadyRegistered == nil {
		t.Error("ErrToolAlreadyRegistered should not be nil")
	}

	// Verify error messages
	if ErrNilTool.Error() != "tool is nil" {
		t.Errorf("ErrNilTool message = %q, want %q", ErrNilTool.Error(), "tool is nil")
	}

	if ErrToolNotFound.Error() != "tool not found" {
		t.Errorf("ErrToolNotFound message = %q, want %q", ErrToolNotFound.Error(), "tool not found")
	}

	if ErrToolAlreadyRegistered.Error() != "tool already registered" {
		t.Errorf("ErrToolAlreadyRegistered message = %q, want %q", ErrToolAlreadyRegistered.Error(), "tool already registered")
	}
}

// TestRegistryFilterEdgeCases tests edge cases for filtering.
func TestRegistryFilterEdgeCases(t *testing.T) {
	registry := NewRegistry()

	tool := &MockTool{
		name:        "edge_case_tool",
		description: "A tool for edge case test",
		category:    CategoryCore,
	}
	_ = registry.Register(tool)

	tests := []struct {
		name      string
		filter    *ToolFilter
		wantCount int
	}{
		{
			name: "filter with both enabled and disabled",
			filter: &ToolFilter{
				Enabled:  []string{"edge_case_tool"},
				Disabled: []string{"edge_case_tool"},
			},
			wantCount: 0, // Disabled takes precedence
		},
		{
			name: "filter with non-existing enabled",
			filter: &ToolFilter{
				Enabled: []string{"non_existing_tool"},
			},
			wantCount: 0,
		},
		{
			name: "filter with non-existing disabled",
			filter: &ToolFilter{
				Disabled: []string{"non_existing_tool"},
			},
			wantCount: 1,
		},
		{
			name: "filter with empty enabled list",
			filter: &ToolFilter{
				Enabled: []string{},
			},
			wantCount: 1,
		},
		{
			name: "filter with empty disabled list",
			filter: &ToolFilter{
				Disabled: []string{},
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := registry.Filter(tt.filter)
			if filtered.Count() != tt.wantCount {
				t.Errorf("filtered count = %d, want %d", filtered.Count(), tt.wantCount)
			}
		})
	}
}
