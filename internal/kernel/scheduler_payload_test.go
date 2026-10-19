package kernel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fabricagent "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestCarryTaskScopedPayload pins the narrow exception to the anti-pollution rule: a task-scoped
// key the executor wrote survives the re-wrap, everything else is still dropped.
//
// Without this, need_tools widens the toolbelt for exactly one quantum — the next one rebuilds
// the envelope from submission-time metadata and re-derives the narrow set.
func TestCarryTaskScopedPayload(t *testing.T) {
	base := map[string]any{"session": "s1"}
	from := map[string]any{
		fabricagent.TaskToolCapabilityKey: []string{"network", "git"},
		"checkpoint":                      "quantum-scoped, must NOT persist",
	}

	got := carryTaskScopedPayload(base, from)

	assert.Equal(t, []string{"network", "git"}, got[fabricagent.TaskToolCapabilityKey],
		"a task-scoped key written by the executor must survive the re-wrap")
	assert.NotContains(t, got, "checkpoint",
		"quantum-scoped keys must still be dropped: carrying them would pollute the durable envelope")
	assert.Equal(t, "s1", got["session"], "the submission-time base is preserved")

	// Copy on write: the caller's map must not be mutated in place.
	assert.NotContains(t, base, fabricagent.TaskToolCapabilityKey)
}

// TestCarryTaskScopedPayload_NoWritesIsANoOp: an executor that wrote nothing must leave the
// envelope exactly as it was (identity, not just equality — the common path allocates nothing).
func TestCarryTaskScopedPayload_NoWritesIsANoOp(t *testing.T) {
	base := map[string]any{"session": "s1"}
	got := carryTaskScopedPayload(base, map[string]any{"checkpoint": "x"})
	assert.Equal(t, base, got)
	assert.Nil(t, carryTaskScopedPayload(nil, nil))
}

// TestCheckpointRoundTrip_CarriesCapabilitiesToTheNextQuantum is the cross-quantum claim the
// whitelist exists for, exercised through the REAL encode/decode boundary rather than by
// comparing two maps in memory.
//
// Sequence: submission-time envelope → executor writes need_tools caps into its copy → re-wrap
// (the yield path) → decode as the next quantum would. If the whitelist were missing, step 3
// would rebuild from the submission payload and the caps would be gone.
func TestCheckpointRoundTrip_CarriesCapabilitiesToTheNextQuantum(t *testing.T) {
	env := taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
		Payload: map[string]any{"session": "s1"},
	})

	submitted, err := taskfabric.DecodeCheckpoint(env)
	require.NoError(t, err)

	// The executor receives a COPY (copyPayloadMap) and writes the widened caps into it.
	executorPayload := map[string]any{"session": "s1"}
	executorPayload[fabricagent.TaskToolCapabilityKey] = []string{"network", "git"}
	executorPayload["checkpoint"] = "quantum-scoped"

	rewrapped := taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
		Payload: carryTaskScopedPayload(submitted.Payload, executorPayload),
	})

	next, err := taskfabric.DecodeCheckpoint(rewrapped)
	require.NoError(t, err)

	assert.Equal(t, []string{"network", "git"}, next.Payload[fabricagent.TaskToolCapabilityKey],
		"the capabilities must be visible to the NEXT quantum, not just to the one that asked")
	assert.NotContains(t, next.Payload, "checkpoint",
		"and the quantum-scoped key must still not reach the durable envelope")
}
