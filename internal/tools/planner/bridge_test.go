package planner

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTool implements core.Tool for testing.
type mockTool struct {
	name    string
	execute func(ctx context.Context, params map[string]interface{}) (core.Result, error)
}

func (m *mockTool) Name() string                      { return m.name }
func (m *mockTool) Description() string               { return "mock " + m.name }
func (m *mockTool) Category() core.ToolCategory       { return core.CategoryCore }
func (m *mockTool) Capabilities() []core.Capability   { return nil }
func (m *mockTool) Parameters() *core.ParameterSchema { return nil }
func (m *mockTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	if m.execute != nil {
		return m.execute(ctx, params)
	}
	return core.Result{Success: true}, nil
}

func TestToolExecutionBridge_DirectExecution(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&mockTool{name: "calculator"}))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	result, err := bridge.Execute(context.Background(), "calculator", map[string]interface{}{
		"expression": "1+1",
	}, "")
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestToolExecutionBridge_ToolNotFoundNoFallback(t *testing.T) {
	reg := core.NewRegistry()
	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	result, err := bridge.Execute(context.Background(), "nonexistent", nil, "")
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, err.Error(), "not found")
}

func TestToolExecutionBridge_PlannerFallback(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&mockTool{name: "calculator"}))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	// Empty tool name + user request triggers planner fallback.
	result, err := bridge.Execute(context.Background(), "", nil, "计算1+1")
	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestToolExecutionBridge_PlannerFallbackMergesParams(t *testing.T) {
	reg := core.NewRegistry()
	var receivedParams map[string]interface{}
	require.NoError(t, reg.Register(&mockTool{
		name: "calculator",
		execute: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			receivedParams = params
			return core.Result{Success: true}, nil
		},
	}))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	_, err = bridge.Execute(context.Background(), "", map[string]interface{}{
		"expression": "2+2",
	}, "计算")
	require.NoError(t, err)
	require.NotNil(t, receivedParams)
	// User-provided params should be in the merged result.
	val, ok := receivedParams["expression"]
	assert.True(t, ok)
	assert.Equal(t, "2+2", val)
}

func TestToolExecutionBridge_ToolNotFoundWithFallback(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&mockTool{name: "calculator"}))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	// Named tool not found but planner fallback should still work.
	result, err := bridge.Execute(context.Background(), "unknown_tool", nil, "计算")
	require.NoError(t, err)
	assert.True(t, result.Success)
}

// newTestPlanner creates a planner with all default components for testing.
func newTestPlanner() *Planner {
	return NewPlanner(
		NewRuleBasedAnalyzer(),
		NewCapabilityPlanner(),
		NewToolResolver(&mockToolProvider{}),
		NewToolScorer(),
		NewExecutionPlanner(),
		NewMemoryEvidenceStore(),
	)
}
