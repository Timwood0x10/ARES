package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// TestProfileExecutor_RollbackKeepsTheStrategyAttribution locks a gap found while assessing
// patch↔strategy attribution (M8a): the rollback patch used to be built without a
// StrategyID.
//
// That is not cosmetic. The deployment pipeline refuses an unattributed patch as
// unmeasurable — correctly, by its own rule — so a strategy that was applied and then rolled
// back became permanently uncomparable: the undo erased the attribution that made the change
// judgeable in the first place.
func TestProfileExecutor_RollbackKeepsTheStrategyAttribution(t *testing.T) {
	store := NewProfileStore()
	require.NoError(t, store.Update(&agents.AgentProfile{
		ID: "p-coder", Role: "coder", Instructions: "old instructions",
	}))

	exec := NewProfileExecutor(store)
	rollback, err := exec.Apply(context.Background(), patch.RuntimePatch{
		Type:       patch.PatchChangeInstruction,
		Target:     "profile:coder",
		Value:      "new instructions",
		StrategyID: "strategy-1",
	})
	require.NoError(t, err)
	require.NotNil(t, rollback, "a change that changes something must produce a rollback patch")

	assert.Equal(t, "strategy-1", rollback.StrategyID,
		"the rollback belongs to the same strategy as the change it undoes")
	assert.Equal(t, "old instructions", rollback.Value,
		"the rollback must restore what was there")
}
