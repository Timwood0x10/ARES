package planner

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaCheckingTool validates that the parameters match expected schema.
type schemaCheckingTool struct {
	name     string
	required []string
	received map[string]interface{}
}

func (m *schemaCheckingTool) Name() string                    { return m.name }
func (m *schemaCheckingTool) Description() string             { return "schema-checking mock" }
func (m *schemaCheckingTool) Category() core.ToolCategory     { return core.CategoryCore }
func (m *schemaCheckingTool) Capabilities() []core.Capability { return nil }
func (m *schemaCheckingTool) Parameters() *core.ParameterSchema {
	props := make(map[string]*core.Parameter)
	for _, r := range m.required {
		props[r] = &core.Parameter{Type: "string", Description: r}
	}
	return &core.ParameterSchema{
		Type:       "object",
		Properties: props,
		Required:   m.required,
	}
}
func (m *schemaCheckingTool) Execute(_ context.Context, params map[string]interface{}) (core.Result, error) {
	m.received = params
	// Verify all required params are present.
	for _, r := range m.required {
		if _, ok := params[r]; !ok {
			return core.NewErrorResult("missing required param: " + r), nil
		}
	}
	return core.NewResult(true, map[string]interface{}{
		"received_params": params,
	}), nil
}

// fixedOutputTool returns predefined data for dependency binding tests.
type fixedOutputTool struct {
	name string
	data map[string]interface{}
}

func (m *fixedOutputTool) Name() string                    { return m.name }
func (m *fixedOutputTool) Description() string             { return "fixed-output mock" }
func (m *fixedOutputTool) Category() core.ToolCategory     { return core.CategoryCore }
func (m *fixedOutputTool) Capabilities() []core.Capability { return nil }
func (m *fixedOutputTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{Type: "object"}
}
func (m *fixedOutputTool) Execute(_ context.Context, _ map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, m.data), nil
}

func TestToolExecutionBridge_SingleStepMergesPlanParams(t *testing.T) {
	reg := core.NewRegistry()
	schemaTool := &schemaCheckingTool{
		name:     "calculator",
		required: []string{"expression"},
	}
	require.NoError(t, reg.Register(schemaTool))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	// Fallback: no LLM tool name, planner generates plan.
	result, err := bridge.Execute(context.Background(), "", nil, "计算1+1")
	require.NoError(t, err)
	require.True(t, result.Success, "planner fallback should succeed: %v", result.Error)

	// The tool should have received the expression param from plan defaults.
	data := result.Data.(map[string]interface{})
	received, ok := data["received_params"].(map[string]interface{})
	require.True(t, ok)
	_, hasExpr := received["expression"]
	assert.True(t, hasExpr, "plan should provide expression param")
}

func TestToolExecutionBridge_PlannerFallbackUserParamsWin(t *testing.T) {
	reg := core.NewRegistry()
	schemaTool := &schemaCheckingTool{
		name:     "calculator",
		required: []string{"expression"},
	}
	require.NoError(t, reg.Register(schemaTool))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	// User provides explicit params that override plan defaults.
	result, err := bridge.Execute(context.Background(), "",
		map[string]interface{}{"expression": "2+2"}, "计算")
	require.NoError(t, err)
	require.True(t, result.Success)

	data := result.Data.(map[string]interface{})
	received, ok := data["received_params"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "2+2", received["expression"],
		"user expression should override plan default")
}

func TestToolExecutionBridge_MultiStepBindsOutputToDownstreamSchema(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&fixedOutputTool{
		name: "pdf_tool",
		data: map[string]interface{}{"text": "extracted body"},
	}))
	downstream := &schemaCheckingTool{
		name:     "string_utils",
		required: []string{"input"},
	}
	require.NoError(t, reg.Register(downstream))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	plan := &ExecutionPlan{
		PlanID:      "test-plan",
		IsMultiStep: true,
		Steps: []ExecutionStep{
			{
				StepID:         "extract",
				ToolName:       "pdf_tool",
				CapabilityName: "PDFParsing",
			},
			{
				StepID:         "process",
				ToolName:       "string_utils",
				CapabilityName: "StringManipulation",
				Parameters:     map[string]interface{}{"operation": "upper", "input": ""},
				DependsOn:      []string{"extract"},
			},
		},
	}

	result, err := bridge.executeMultiStep(context.Background(), plan, nil)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.NotNil(t, downstream.received)
	assert.Equal(t, "extracted body", downstream.received["input"])
}

func TestToolExecutionBridge_MultiStepUserParamsOverrideDependencyBinding(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&fixedOutputTool{
		name: "pdf_tool",
		data: map[string]interface{}{"text": "dependency body"},
	}))
	downstream := &schemaCheckingTool{
		name:     "string_utils",
		required: []string{"input"},
	}
	require.NoError(t, reg.Register(downstream))

	planner := newTestPlanner()
	bridge, err := NewToolExecutionBridge(reg, planner)
	require.NoError(t, err)

	plan := &ExecutionPlan{
		PlanID:      "test-plan",
		IsMultiStep: true,
		Steps: []ExecutionStep{
			{
				StepID:         "extract",
				ToolName:       "pdf_tool",
				CapabilityName: "PDFParsing",
			},
			{
				StepID:         "process",
				ToolName:       "string_utils",
				CapabilityName: "StringManipulation",
				Parameters:     map[string]interface{}{"operation": "upper", "input": ""},
				DependsOn:      []string{"extract"},
			},
		},
	}

	result, err := bridge.executeMultiStep(context.Background(), plan, map[string]interface{}{"input": "manual body"})
	require.NoError(t, err)
	require.True(t, result.Success)
	require.NotNil(t, downstream.received)
	assert.Equal(t, "manual body", downstream.received["input"])
}
