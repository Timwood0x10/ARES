package planner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
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
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
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
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
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
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
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
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
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
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	// Named tool not found but planner fallback should still work.
	result, err := bridge.Execute(context.Background(), "unknown_tool", nil, "计算")
	require.NoError(t, err)
	assert.True(t, result.Success)
}

// newTestPlanner creates a planner with all default components for testing.
func newTestPlanner() *Planner {
	resolver, err := NewToolResolver(&mockToolProvider{})
	if err != nil {
		panic(err)
	}
	store := NewMemoryEvidenceStore()
	planner, err := NewPlanner(
		NewRuleBasedAnalyzer(),
		NewCapabilityPlanner(),
		resolver,
		NewEvidenceScorer(store),
		NewExecutionPlanner(),
		store,
	)
	if err != nil {
		panic(err)
	}
	return planner
}

// TestToolExecutionBridge_ExecutePlan_HardBlocksDuplicateStepIDs is the #50
// regression: Execute hard-blocks duplicate_id/empty_id DAG errors, but
// ExecutePlan (the pre-built-plan path) treated them as advisory warnings.
// A plan with two steps sharing one StepID silently executed the first step
// twice and skipped the second (executeMultiStep looks steps up by ID).
func TestToolExecutionBridge_ExecutePlan_HardBlocksDuplicateStepIDs(t *testing.T) {
	reg := core.NewRegistry()
	executed := make([]string, 0, 4)
	require.NoError(t, reg.Register(&mockTool{name: "stepA", execute: func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
		executed = append(executed, "A")
		return core.Result{Success: true}, nil
	}}))
	require.NoError(t, reg.Register(&mockTool{name: "stepB", execute: func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
		executed = append(executed, "B")
		return core.Result{Success: true}, nil
	}}))
	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	plan := &ExecutionPlan{
		PlanID: "dup-plan",
		Steps: []ExecutionStep{
			{StepID: "s1", ToolName: "stepA"},
			{StepID: "s1", ToolName: "stepB"}, // duplicate ID — must hard-block
		},
	}

	_, err = bridge.ExecutePlan(context.Background(), plan, nil)
	require.Error(t, err, "duplicate StepID must hard-block ExecutePlan")
	assert.Contains(t, err.Error(), "duplicate_id")
	assert.Empty(t, executed, "no step may run for an invalid DAG")
}

// TestToolExecutionBridge_ExecutePlan_HardBlocksEmptyStepIDs is the #50
// regression for empty_id: an empty StepID cannot be addressed by
// dependencies or results and must hard-block, not warn.
func TestToolExecutionBridge_ExecutePlan_HardBlocksEmptyStepIDs(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&mockTool{name: "stepA"}))
	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	plan := &ExecutionPlan{
		PlanID: "empty-plan",
		Steps: []ExecutionStep{
			{StepID: "", ToolName: "stepA"},
		},
	}

	_, err = bridge.ExecutePlan(context.Background(), plan, nil)
	require.Error(t, err, "empty StepID must hard-block ExecutePlan")
	assert.Contains(t, err.Error(), "empty_id")
}

// TestToolExecutionBridge_ExecutePlan_ValidPlanStillRuns guards against
// over-blocking: a valid single-step plan must execute after the new checks.
func TestToolExecutionBridge_ExecutePlan_ValidPlanStillRuns(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&mockTool{name: "stepA"}))
	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	plan := &ExecutionPlan{
		PlanID: "ok-plan",
		Steps: []ExecutionStep{
			{StepID: "s1", ToolName: "stepA"},
			{StepID: "s2", ToolName: "stepA", DependsOn: []string{"s1"}},
		},
	}
	result, err := bridge.ExecutePlan(context.Background(), plan, nil)
	require.NoError(t, err)
	assert.True(t, result.Success)
}
