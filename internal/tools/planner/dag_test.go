package planner

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestDAGValidator_NilPlan(t *testing.T) {
    v := NewDAGValidator()
    errs := v.Validate(nil)
    require.Len(t, errs, 1)
    assert.Contains(t, errs[0].Error(), "nil_plan")
}

func TestDAGValidator_EmptySteps(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{Steps: []ExecutionStep{}}
    errs := v.Validate(plan)
    require.Len(t, errs, 1)
    assert.Contains(t, errs[0].Error(), "no_steps")
}

func TestDAGValidator_ValidSingleStep(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        Steps: []ExecutionStep{
            {StepID: "step-1", ToolName: "calculator", CapabilityName: "Arithmetic"},
        },
    }
    errs := v.Validate(plan)
    assert.Nil(t, errs)
}

func TestDAGValidator_ValidMultiStep(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        IsMultiStep: true,
        Steps: []ExecutionStep{
            {StepID: "step-pdf", ToolName: "pdf_tool", CapabilityName: "PDFParsing"},
            {StepID: "step-text", ToolName: "string_utils", CapabilityName: "StringManipulation",
                DependsOn: []string{"step-pdf"}},
        },
    }
    errs := v.Validate(plan)
    assert.Nil(t, errs)
}

func TestDAGValidator_MissingDependency(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        Steps: []ExecutionStep{
            {StepID: "step-2", DependsOn: []string{"step-nonexistent"}},
        },
    }
    errs := v.Validate(plan)
    require.GreaterOrEqual(t, len(errs), 1)
    foundMissing := false
    for _, e := range errs {
        if e.Code == "missing_dependency" {
            foundMissing = true
        }
    }
    assert.True(t, foundMissing, "should have missing_dependency error")
}

func TestDAGValidator_CycleDetection(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        Steps: []ExecutionStep{
            {StepID: "step-a", DependsOn: []string{"step-b"}},
            {StepID: "step-b", DependsOn: []string{"step-c"}},
            {StepID: "step-c", DependsOn: []string{"step-a"}},
        },
    }
    errs := v.Validate(plan)
    require.Len(t, errs, 1)
    assert.Contains(t, errs[0].Code, "cycle_detected")
}

func TestDAGValidator_SelfCycle(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        Steps: []ExecutionStep{
            {StepID: "step-a", DependsOn: []string{"step-a"}},
        },
    }
    errs := v.Validate(plan)
    require.Len(t, errs, 1)
    assert.Contains(t, errs[0].Code, "cycle_detected")
}

func TestDAGValidator_DuplicateStepID(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        Steps: []ExecutionStep{
            {StepID: "step-1"},
            {StepID: "step-1"},
        },
    }
    errs := v.Validate(plan)
    require.Len(t, errs, 1)
    assert.Contains(t, errs[0].Code, "duplicate_id")
}

func TestDAGValidator_EmptyStepID(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        Steps: []ExecutionStep{
            {StepID: ""},
        },
    }
    errs := v.Validate(plan)
    require.Len(t, errs, 1)
    assert.Contains(t, errs[0].Code, "empty_id")
}

func TestDAGValidator_IOCompatibility(t *testing.T) {
    v := NewDAGValidator()
    plan := &ExecutionPlan{
        IsMultiStep: true,
        Steps: []ExecutionStep{
            {StepID: "step-pdf", CapabilityName: "PDFParsing"},
            {StepID: "step-math", CapabilityName: "Arithmetic",
                DependsOn: []string{"step-pdf"}},
        },
    }
    errs := v.Validate(plan)
    // IO incompatibility is advisory; should not be a missing dep or cycle.
    for _, e := range errs {
        assert.NotEqual(t, "missing_dependency", e.Code)
        assert.NotEqual(t, "cycle_detected", e.Code)
    }
}

func TestDAGValidator_TopologicalOrderValid(t *testing.T) {
    v := NewDAGValidator()
    // Compatible I/O chain: PDFParsing(Text) → StringManipulation(Text).
    plan := &ExecutionPlan{
        IsMultiStep: true,
        Steps: []ExecutionStep{
            {StepID: "extract", CapabilityName: "PDFParsing"},
            {StepID: "process", CapabilityName: "StringManipulation",
                DependsOn: []string{"extract"}},
        },
    }
    errs := v.Validate(plan)
    assert.Nil(t, errs)
}
