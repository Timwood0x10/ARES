package agentruntime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	kctx "github.com/Timwood0x10/ares/internal/kernel/ctx"
)

// recordingCognition captures the identity the executor stamped before delegating, which
// is the security-relevant half of the adapter: the Kernel syscalls (spawn_agent /
// create_task) enforce task provenance from ctx.CallerID(), so an unstamped call would let
// a model-supplied identity stand in for the executing agent's.
type recordingCognition struct {
	seenCallerID string
	outcome      *agentfabric.StepOutcome
}

func (c *recordingCognition) ExecuteStep(ctx context.Context, _ *models.Task) (*agentfabric.StepOutcome, error) {
	c.seenCallerID = kctx.CallerID(ctx)
	return c.outcome, nil
}

// TestRouterExecutor_StampsTheIdentityBeforeDelegating: the adapter must not simply pass
// the context through. Serve learned this the hard way (the serve path fell back to the
// LLM-supplied parent id, which is forgeable); this pins it for the evaluation runtime too.
func TestRouterExecutor_StampsTheIdentityBeforeDelegating(t *testing.T) {
	cog := &recordingCognition{}
	executor, err := NewRouterExecutor(&Execution{Router: cog}, "candidate-plan", PlanCapability)
	require.NoError(t, err)

	out, err := executor.ExecuteStep(context.Background(), &models.Task{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "candidate-plan", cog.seenCallerID,
		"the executing identity must be stamped: syscall provenance is read from it")
	assert.Equal(t, "candidate-plan", executor.ID())
	assert.Equal(t, models.AgentType(PlanCapability), executor.Type())
}

// TestRouterExecutor_TranslatesANilOutcome: a cognition that returns nothing must become
// an empty outcome rather than a nil dereference in the scheduler.
func TestRouterExecutor_TranslatesANilOutcome(t *testing.T) {
	executor, err := NewRouterExecutor(&Execution{Router: &recordingCognition{}}, "x", PlanCapability)
	require.NoError(t, err)

	out, err := executor.ExecuteStep(context.Background(), &models.Task{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.False(t, out.Done)
}

// TestNewRouterExecutor_FailsLoudOnMissingPieces: an executor that cannot execute would
// let the scheduler lease a task to a body that does nothing, so the case would stall
// instead of naming the missing piece.
func TestNewRouterExecutor_FailsLoudOnMissingPieces(t *testing.T) {
	_, err := NewRouterExecutor(nil, "id", PlanCapability)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assembled execution core")

	_, err = NewRouterExecutor(&Execution{}, "id", PlanCapability)
	require.Error(t, err, "a core without a router cannot back an executor")

	_, err = NewRouterExecutor(&Execution{Router: &recordingCognition{}}, "", PlanCapability)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity")
}

// TestDefaultCandidateExecutors_CoversThePlanCapability: the default set is what makes a
// caller able to assemble a runnable evaluation environment by supplying only a chat
// client and a tool binder — and it must cover the capability a case is submitted with,
// or every case would sit READY.
func TestDefaultCandidateExecutors_CoversThePlanCapability(t *testing.T) {
	executors, err := DefaultCandidateExecutors(&Execution{Router: &recordingCognition{}})
	require.NoError(t, err)
	require.Len(t, executors, 1)

	var coversPlan bool
	for _, executor := range executors {
		if executor.Type() == PlanCapability {
			coversPlan = true
		}
	}
	assert.True(t, coversPlan, "no executor can take the plan capability: every case would stall")
}
