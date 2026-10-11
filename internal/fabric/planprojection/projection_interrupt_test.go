package planprojection

import (
	"testing"

	"github.com/stretchr/testify/assert"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// TestProjectStep_MapsInterrupt verifies that engine.Step.Interrupt is mapped
// to PlanStep.RequiresApproval/ApprovalMessage/ApprovalPayload.
func TestProjectStep_MapsInterrupt(t *testing.T) {
	s := &engine.Step{
		ID:        "deploy",
		AgentType: "tool/shell",
		Interrupt: &engine.InterruptConfig{
			Message: "approve before deploy",
			Payload: map[string]any{"risk": "high"},
		},
	}

	ps := ProjectStep(s)
	assert.True(t, ps.RequiresApproval)
	assert.Equal(t, "approve before deploy", ps.ApprovalMessage)
	assert.Equal(t, map[string]any{"risk": "high"}, ps.ApprovalPayload)
}

// TestProjectStep_NoInterruptStaysStrict verifies that a step without
// Interrupt produces zero-value approval fields.
func TestProjectStep_NoInterruptStaysStrict(t *testing.T) {
	s := &engine.Step{
		ID:        "safe-step",
		AgentType: "tool/shell",
	}

	ps := ProjectStep(s)
	assert.False(t, ps.RequiresApproval)
	assert.Equal(t, "", ps.ApprovalMessage)
	assert.Nil(t, ps.ApprovalPayload)
}

// TestProjectStep_NilStepReturnsZero verifies that a nil step returns a
// zero PlanStep without panicking.
func TestProjectStep_NilStepReturnsZero(t *testing.T) {
	ps := ProjectStep(nil)
	assert.False(t, ps.RequiresApproval)
}

// TestProjectSteps_BatchPreservesOrder verifies that ProjectSteps preserves
// input order and maps interrupt fields across a batch.
func TestProjectSteps_BatchPreservesOrder(t *testing.T) {
	steps := []*engine.Step{
		{ID: "a", AgentType: "rust"},
		{ID: "b", AgentType: "rust", Interrupt: &engine.InterruptConfig{Message: "check b"}},
		{ID: "c", AgentType: "rust"},
	}

	result := ProjectSteps(steps)
	if len(result) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(result))
	}
	assert.Equal(t, "a", result[0].ID)
	assert.Equal(t, "b", result[1].ID)
	assert.Equal(t, "c", result[2].ID)
	assert.False(t, result[0].RequiresApproval)
	assert.True(t, result[1].RequiresApproval)
	assert.False(t, result[2].RequiresApproval)
}

// Ensure the taskfabric import is used.
var _ taskfabric.PlanStep
