package workflow

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

type recordingEngineStepExecutor struct {
	inputs map[string]string
}

func (e *recordingEngineStepExecutor) Execute(
	_ context.Context,
	step *engine.Step,
	input string,
	_ *models.TaskContext,
) (string, error) {
	e.inputs[step.ID] = input
	return step.ID + "-output", nil
}

func TestEngineNodeExecutor_PreservesLegacyInputResolution(t *testing.T) {
	t.Parallel()

	steps := []*engine.Step{
		{ID: "source", AgentType: "test"},
		{
			ID:        "templated",
			AgentType: "test",
			DependsOn: []string{"source"},
			Input:     "initial={{.input}}; source={{.source}}",
		},
		{
			ID:        "fallback",
			AgentType: "test",
			DependsOn: []string{"source", "templated"},
		},
	}
	workflowDef := &engine.Workflow{ID: "engine-adapter", Steps: steps}
	compiled, err := CompileFromEngineWithBindings(workflowDef)
	if err != nil {
		t.Fatalf("CompileFromEngineWithBindings() error = %v", err)
	}
	bound, err := BindCompiledWorkflow(compiled)
	if err != nil {
		t.Fatalf("BindCompiledWorkflow() error = %v", err)
	}
	recorder := &recordingEngineStepExecutor{inputs: make(map[string]string)}
	executor, err := NewEngineNodeExecutorWithStepExecutor(recorder, steps)
	if err != nil {
		t.Fatalf("NewEngineNodeExecutorWithStepExecutor() error = %v", err)
	}

	result, err := NewRunner(executor, WithInitialInput("request")).ExecuteBound(context.Background(), bound)
	if err != nil {
		t.Fatalf("ExecuteBound() error = %v", err)
	}
	if result.Status != NodeStatusCompleted {
		t.Fatalf("result status = %q, want %q", result.Status, NodeStatusCompleted)
	}
	if recorder.inputs["source"] != "request" {
		t.Fatalf("source input = %q, want request", recorder.inputs["source"])
	}
	if recorder.inputs["templated"] != "initial=request; source=source-output" {
		t.Fatalf("templated input = %q", recorder.inputs["templated"])
	}
	if recorder.inputs["fallback"] != "source-output\n\ntemplated-output" {
		t.Fatalf("fallback input = %q", recorder.inputs["fallback"])
	}
}

func TestNewEngineNodeExecutorWithStepExecutor_RejectsInvalidBindings(t *testing.T) {
	t.Parallel()

	recorder := &recordingEngineStepExecutor{inputs: make(map[string]string)}
	testCases := []struct {
		name  string
		steps []*engine.Step
	}{
		{name: "nil step", steps: []*engine.Step{nil}},
		{name: "empty ID", steps: []*engine.Step{{AgentType: "test"}}},
		{name: "duplicate ID", steps: []*engine.Step{{ID: "same"}, {ID: "same"}}},
	}
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewEngineNodeExecutorWithStepExecutor(recorder, testCase.steps); err == nil {
				t.Fatal("expected invalid binding error")
			}
		})
	}
}
