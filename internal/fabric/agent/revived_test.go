package agentfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestExecuteStepStampsRevivedBit locks the A6 contract: an agent whose
// cognitive state was installed by Recover (the only revive entry point)
// stamps executor_revived=true on every quantum's payload, while a normal
// agent stamps false. "Present and false" must be distinguishable from
// "absent" — a quantum that did not run through the wrapper never answered
// the question at all.
func TestExecuteStepStampsRevivedBit(t *testing.T) {
	t.Run("revived agent stamps true", func(t *testing.T) {
		f := NewFabric()
		cog := &stubCognition{out: &StepOutcome{Done: true,
			Result: models.NewTaskResult("t1", "code")}}
		_, err := f.Spawn(context.Background(), SpawnSpec{
			Identity: "worker-1", Capabilities: []string{"code"},
			CognitionFactory: func([]string) Cognition { return cog },
		})
		require.NoError(t, err)

		// Recover installs a preserved cognitive state → sets revived.
		require.NoError(t, f.Recover(context.Background(), "worker-1", CognitiveState{
			SchemaVersion: CognitiveStateSchemaVersion,
		}))

		a, err := f.Get("worker-1")
		require.NoError(t, err)

		task := models.NewTask("t1", "code", nil)
		_, err = a.ExecuteStep(context.Background(), task)
		require.NoError(t, err)

		got, ok := task.Payload[executorRevivedKey].(bool)
		require.True(t, ok, "executor_revived must be present (true)")
		assert.True(t, got, "a revived agent stamps executor_revived=true")
	})

	t.Run("normal agent stamps false", func(t *testing.T) {
		f := NewFabric()
		cog := &stubCognition{out: &StepOutcome{Done: true,
			Result: models.NewTaskResult("t2", "code")}}
		_, err := f.Spawn(context.Background(), SpawnSpec{
			Identity: "worker-2", Capabilities: []string{"code"},
			CognitionFactory: func([]string) Cognition { return cog },
		})
		require.NoError(t, err)

		a, err := f.Get("worker-2")
		require.NoError(t, err)

		task := models.NewTask("t2", "code", nil)
		_, err = a.ExecuteStep(context.Background(), task)
		require.NoError(t, err)

		got, ok := task.Payload[executorRevivedKey].(bool)
		require.True(t, ok, "executor_revived must be present (false)")
		assert.False(t, got, "a non-revived agent stamps executor_revived=false")
	})
}

// TestRecoverSetsRevivedAndNeverClears locks the lifecycle invariant: Recover
// is the ONLY path that sets revived, and once set it is never cleared — a
// revived agent stays revived for its whole life.
func TestRecoverSetsRevivedAndNeverClears(t *testing.T) {
	f := NewFabric()
	_, err := f.Spawn(context.Background(), SpawnSpec{
		Identity: "agent-r", Capabilities: []string{"code"},
		CognitionFactory: func([]string) Cognition { return &stubCognition{} },
	})
	require.NoError(t, err)

	// Before recover: not revived.
	a, _ := f.Get("agent-r")
	assert.False(t, a.revived, "a fresh agent is not revived")

	// Recover sets it.
	require.NoError(t, f.Recover(context.Background(), "agent-r", CognitiveState{
		SchemaVersion: CognitiveStateSchemaVersion,
	}))
	a, _ = f.Get("agent-r")
	assert.True(t, a.revived, "Recover sets revived=true")

	// Resume from PAUSED does not clear it.
	require.NoError(t, f.Suspend(context.Background(), "agent-r"))
	require.NoError(t, f.Resume(context.Background(), "agent-r"))
	a, _ = f.Get("agent-r")
	assert.True(t, a.revived, "revived survives Suspend→Resume")
}

// TestRecoverEventCarriesRevivedFlag locks the event contract: the
// EventAgentRecovered event carries revived=true so external consumers
// (observability, audit) can tell a recovery from a fresh spawn.
func TestRecoverEventCarriesRevivedFlag(t *testing.T) {
	sink := &recordingSink{}
	f := NewFabric().WithEventSink(sink)
	_, err := f.Spawn(context.Background(), SpawnSpec{
		Identity: "agent-ev", Capabilities: []string{"code"},
		CognitionFactory: func([]string) Cognition { return &stubCognition{} },
	})
	require.NoError(t, err)

	require.NoError(t, f.Recover(context.Background(), "agent-ev", CognitiveState{
		SchemaVersion: CognitiveStateSchemaVersion,
	}))

	var revivedFlag any
	sink.mu.Lock()
	for _, ev := range sink.events {
		if ev.Type == EventAgentRecovered {
			revivedFlag = ev.Payload["revived"]
		}
	}
	sink.mu.Unlock()
	assert.Equal(t, true, revivedFlag,
		"EventAgentRecovered must carry revived=true")
}
