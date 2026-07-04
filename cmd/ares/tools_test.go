package main

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// mockTool is a minimal tool for testing the binder bridge.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string                      { return m.name }
func (m *mockTool) Description() string               { return "mock " + m.name }
func (m *mockTool) Category() core.ToolCategory       { return core.CategoryCore }
func (m *mockTool) Capabilities() []core.Capability   { return nil }
func (m *mockTool) Parameters() *core.ParameterSchema { return nil }
func (m *mockTool) Execute(_ context.Context, _ map[string]interface{}) (core.Result, error) {
	return core.Result{Success: true}, nil
}

// TestNewToolRegistry verifies the public tool registry is created correctly.
func TestNewToolRegistry(t *testing.T) {
	reg, err := newToolRegistry()
	if err != nil {
		t.Fatalf("newToolRegistry() failed: %v", err)
	}
	if reg == nil {
		t.Fatal("newToolRegistry() returned nil")
	}

	tools := reg.List()
	if len(tools) == 0 {
		t.Error("expected at least one built-in tool, got none")
	}

	// Verify a few expected built-in tools exist.
	expected := []string{"calculator", "hash_tool", "regex", "json_tools", "file_tools"}
	for _, name := range expected {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("expected built-in tool %q not found", name)
		}
	}
}

// TestNewToolBinder verifies the tool binder bridges from core.Registry correctly.
func TestNewToolBinder(t *testing.T) {
	internalReg := core.NewRegistry()
	// Register a test tool so the binder has something to bridge.
	_ = internalReg.Register(&mockTool{name: "test_tool"})
	binder := newToolBinder(internalReg)
	if binder == nil {
		t.Fatal("newToolBinder() returned nil")
	}

	tools := binder.ListTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool in binder after bridging")
	}
	found := false
	for _, name := range tools {
		if name == "test_tool" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected bridged tool 'test_tool' to appear in binder")
	}
}

// TestNewPlannerBridge verifies the planner bridge can be created without error.
func TestNewPlannerBridge(t *testing.T) {
	internalReg := core.NewRegistry()
	bridge := newPlannerBridge(internalReg)
	if bridge == nil {
		t.Fatal("newPlannerBridge() returned nil")
	}
}

// TestNewPlannerBridgeWithToolBinder verifies the planner bridge integrates
// with the tool binder via WithPlannerBridge.
func TestNewPlannerBridgeWithToolBinder(t *testing.T) {
	internalReg := core.NewRegistry()
	bridge := newPlannerBridge(internalReg)
	if bridge == nil {
		t.Fatal("newPlannerBridge() returned nil")
	}

	binder := newToolBinder(internalReg)
	// WithPlannerBridge should accept the bridge without panicking.
	binder.WithPlannerBridge(bridge)
}
