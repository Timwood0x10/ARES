// Package ares_bootstrap — Runtime Closure Feedback Loop Tests (Stage 4).
//
// These tests verify the real data flow across the feedback chain
// Event → Evidence → GA → Strategy → Agent. Unlike earlier stage tests that
// check wiring identity, these assert that emitting an event produces
// observable evidence in the shared EvidenceStore, and that a strategy
// written to the shared StrategyStore is readable through the Agent's
// StrategySource — i.e. the loop moves real data, not just references.
//
//go:build closure

package ares_bootstrap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFeedbackLoopComponents builds a Bootstrap instance with evolution enabled
// so the flight recorder, evidence store, and GA strategy store are wired.
func newFeedbackLoopComponents(t *testing.T) (*Components, context.CancelFunc) {
	t.Helper()
	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
		Memory:    ares_config.MemoryConfig{Enabled: true},
		Evolution: ares_config.EvolutionConfig{Enabled: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err, "Bootstrap must succeed")
	require.NotNil(t, comp)
	require.NotNil(t, comp.EventStore, "EventStore must be wired")
	require.NotNil(t, comp.EvidenceStore, "EvidenceStore must be wired")
	require.NotNil(t, comp.FlightRecorder, "FlightRecorder must be wired when evolution enabled")
	return comp, cancel
}

// emitTaskEvent publishes a task lifecycle event into the shared EventStore,
// which is what the FlightRecorder collector subscribes to.
func emitTaskEvent(t *testing.T, store ares_events.EventStore, eventType ares_events.EventType) {
	t.Helper()
	evt := &ares_events.Event{
		StreamID: "agent-leader",
		Type:     eventType,
		Payload:  map[string]any{"task_id": "task-1"},
		Version:  1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, store.Append(ctx, evt.StreamID, []*ares_events.Event{evt}, 0),
		"Append must succeed")
}

// queryFitness waits (bounded) for the collector to process the event and
// returns fitness evidence for the given source, or fails the test on timeout.
func queryFitness(t *testing.T, store *evidence.MemoryStore, source string) []evidence.Evidence {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		evs, err := store.Query(ctx, evidence.Filter{
			Source: source,
			Kind:   evidence.KindFitness,
			Limit:  10,
		})
		cancel()
		require.NoError(t, err, "EvidenceStore.Query must not fail")
		if len(evs) > 0 {
			return evs
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no %q fitness evidence observed within 5s — feedback loop broken", source)
	return nil
}

// TestClosure_EventToEvidence_WorkflowFitness verifies the first hop of the
// loop: emitting a task.completed event produces workflow fitness evidence in
// the shared EvidenceStore via the FlightRecorder collector (real data flow,
// not just a wired reference).
func TestClosure_EventToEvidence_WorkflowFitness(t *testing.T) {
	comp, cancel := newFeedbackLoopComponents(t)
	defer comp.WaitBackground()
	defer cancel()

	emitTaskEvent(t, comp.EventStore, ares_events.EventTaskCompleted)

	evs := queryFitness(t, comp.EvidenceStore, "workflow")
	assert.NotEmpty(t, evs, "workflow fitness evidence must exist after task.completed")
	assert.Equal(t, 1.0, fitnessValue(t, evs[0]), "completed task must score 1.0")
}

// TestClosure_EventToEvidence_SchedulerFitness verifies the scheduler hop:
// a failed task must produce scheduler fitness evidence scored 0.0.
func TestClosure_EventToEvidence_SchedulerFitness(t *testing.T) {
	comp, cancel := newFeedbackLoopComponents(t)
	defer comp.WaitBackground()
	defer cancel()

	emitTaskEvent(t, comp.EventStore, ares_events.EventTaskFailed)

	evs := queryFitness(t, comp.EvidenceStore, "scheduler")
	assert.NotEmpty(t, evs, "scheduler fitness evidence must exist after task.failed")
	assert.Equal(t, 0.0, fitnessValue(t, evs[0]), "failed task must score 0.0")
}

// TestClosure_StrategyWriteAgentRead verifies the GA → Strategy → Agent hop:
// a strategy written to the shared StrategyStore is readable through the same
// StrategySource the Agent uses at runtime (NewStrategySource wraps the same
// store the GA deploys to).
func TestClosure_StrategyWriteAgentRead(t *testing.T) {
	comp, cancel := newFeedbackLoopComponents(t)
	defer comp.WaitBackground()
	defer cancel()

	require.NotNil(t, comp.NewEvolution, "NewEvolution must be wired when evolution enabled")
	require.NotNil(t, comp.NewEvolution.StrategyStore,
		"StrategyStore must be created by wireGAEvolution")

	// Write the deployed strategy exactly as the GA would.
	strategy := &ares_evolution.Strategy{
		ID:             "gen-9",
		PromptTemplate: "use memory",
		Params:         map[string]any{"k": 1},
		Version:        1,
	}
	ctx, c2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer c2()
	require.NoError(t, comp.NewEvolution.StrategyStore.SetActive(ctx, strategy))

	// The Agent reads through NewStrategySource — the same instance the GA
	// wrote to, proving the write→read hop shares one store.
	src := NewStrategySource(comp.NewEvolution.StrategyStore)
	require.NotNil(t, src, "StrategySource must wrap the shared store")
	active, err := src.GetActiveStrategy(ctx)
	require.NoError(t, err, "GetActiveStrategy must succeed after SetActive")
	require.NotNil(t, active, "active strategy must be readable")
	assert.Equal(t, "gen-9", active.ID, "Agent must read the strategy the GA deployed")
}

// fitnessValue extracts the normalized fitness value ("value" key) from the
// evidence payload, which is a json.RawMessage.
func fitnessValue(t *testing.T, e evidence.Evidence) float64 {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(e.Payload, &payload), "evidence payload must be JSON")
	val, ok := payload["value"].(float64)
	require.True(t, ok, "fitness evidence must carry a numeric \"value\" key")
	return val
}
