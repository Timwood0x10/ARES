package kernel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestToModelTaskDoesNotAliasEnvelopePayload pins audit HIGH #1: the
// executor-facing models.Task must not alias the durable checkpoint
// envelope's Payload map. The executor (and the scheduler itself) write
// quantum-scoped keys into t.Payload ("checkpoint"); an alias would persist
// those keys into the envelope on the next yield/done re-wrap, permanently
// polluting the persisted payload.
func TestToModelTaskDoesNotAliasEnvelopePayload(t *testing.T) {
	sched := New(taskfabric.NewFabric(), nil, nil)

	envelopePayload := map[string]any{"input": "hello", "keep": "me"}
	tk := &taskfabric.Task{
		ID:         "t-alias",
		Capability: "code",
		Checkpoint: taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
			Payload:        envelopePayload,
			SessionID:      "s-1",
			StepCheckpoint: map[string]any{"round": 1},
		}),
	}

	mt := sched.ToModelTask(tk)
	require.NotNil(t, mt.Payload)

	// The executor/scheduler writes quantum-scoped keys into the model task.
	mt.Payload["checkpoint"] = "quantum-scoped-write"
	mt.Payload["executing_agent_id"] = "agent-x"

	// The durable envelope's payload must be untouched.
	assert.NotContains(t, envelopePayload, "checkpoint",
		"a write through the model task must not leak into the envelope's payload map")
	assert.NotContains(t, envelopePayload, "executing_agent_id")
	assert.Equal(t, "hello", envelopePayload["input"],
		"original keys and values must survive untouched")
}
