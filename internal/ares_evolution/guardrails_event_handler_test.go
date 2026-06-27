//go:build goagent_evolution_handler

// This file contains tests for the GuardrailEventHandler callback feature.
// These tests depend on WithGuardrailEventHandler option and eventHandler field
// on EvolutionGuardrails. They are gated behind the build tag
// "goagent_evolution_handler" so they do not block existing tests.
//
// To run these tests once the feature is merged:
//
//	go test -tags goagent_evolution_handler ./internal/ares_evolution/ -run TestGuardrailEventHandler
//
// TODO: Remove the build tag constraint once WithGuardrailEventHandler is merged.

package evolution

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardrailEventHandlerRecorder captures events passed to the handler for
// verification in tests.
type guardrailEventHandlerRecorder struct {
	mu     sync.Mutex
	events []GuardrailEvent
}

func (r *guardrailEventHandlerRecorder) handle(event GuardrailEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *guardrailEventHandlerRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *guardrailEventHandlerRecorder) last() GuardrailEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.events[len(r.events)-1]
}

func (r *guardrailEventHandlerRecorder) all() []GuardrailEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]GuardrailEvent, len(r.events))
	copy(cp, r.events)
	return cp
}

func TestGuardrailEventHandler_CalledOnPreEvolveEvent(t *testing.T) {
	defer discardLogs()()
	rec := &guardrailEventHandlerRecorder{}
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithGuardrailEventHandler(rec.handle),
	)

	ctx := context.Background()
	// Trigger unevaluated population event (critical).
	result := g.PreEvolveCheck(ctx, 75.0, 1, 100, 60)

	require.True(t, result.ShouldStop)
	assert.Equal(t, 1, rec.count(), "handler should be called once for unevaluated population event")
	assert.Equal(t, GuardrailCritical, rec.last().Level)
	assert.Equal(t, "unevaluated_population", rec.last().Rule)
	assert.Equal(t, ErrCodeUnevaluatedPopulation, rec.last().ErrorCode)
}

func TestGuardrailEventHandler_CalledOnPostEvolveEvent(t *testing.T) {
	defer discardLogs()()
	rec := &guardrailEventHandlerRecorder{}
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(85.0),
		WithGuardrailEventHandler(rec.handle),
	)

	ctx := context.Background()
	// Establish best known score above baseline.
	g.PostEvolveCheck(ctx, 90.0, 1, nil)
	// Trigger baseline regression event (critical).
	result := g.PostEvolveCheck(ctx, 80.0, 2, nil)

	require.True(t, result.ShouldStop)
	assert.Equal(t, 1, rec.count(), "handler should be called once for baseline regression event")
	assert.Equal(t, GuardrailCritical, rec.last().Level)
	assert.Equal(t, "baseline_regression", rec.last().Rule)
	assert.Equal(t, ErrCodeBaselineRegression, rec.last().ErrorCode)
	assert.Equal(t, 80.0, rec.last().Score)
}

func TestGuardrailEventHandler_ReceivesCorrectEventData(t *testing.T) {
	defer discardLogs()()
	rec := &guardrailEventHandlerRecorder{}
	g, _ := NewEvolutionGuardrails(
		WithMaxLineageShare(0.5),
		WithGuardrailEventHandler(rec.handle),
	)

	ctx := context.Background()
	lineageShares := map[string]int{"lineage-a": 80, "lineage-b": 10, "lineage-c": 10}
	g.PostEvolveCheck(ctx, 85.0, 3, lineageShares)

	require.Equal(t, 1, rec.count())
	event := rec.last()
	assert.Equal(t, GuardrailWarning, event.Level)
	assert.Equal(t, "lineage_concentration", event.Rule)
	assert.Equal(t, ErrCodeLineageConcentration, event.ErrorCode)
	assert.Equal(t, 85.0, event.Score)
	assert.Equal(t, 3, event.Generation)
	assert.NotZero(t, event.Timestamp)
	assert.NotEmpty(t, event.SuggestedAction)
}

func TestGuardrailEventHandler_NilHandlerDoesNotPanic(t *testing.T) {
	defer discardLogs()()
	// Create guardrails with nil handler (no WithGuardrailEventHandler option).
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(3),
	)

	ctx := context.Background()

	// Trigger events; nil handler must not panic.
	assert.NotPanics(t, func() {
		g.PreEvolveCheck(ctx, 75.0, 1, 100, 60)
	})
	assert.NotPanics(t, func() {
		g.PostEvolveCheck(ctx, 70.0, 2, nil)
	})

	// Stagnation event should also not panic with nil handler.
	for i := 0; i < 4; i++ {
		g.PostEvolveCheck(ctx, 75.0, i+3, nil)
	}
	assert.NotPanics(t, func() {
		g.PreEvolveCheck(ctx, 75.0, 10, 100, 10)
	})
}

func TestGuardrailEventHandler_MultipleEventsInOneCheck(t *testing.T) {
	defer discardLogs()()
	rec := &guardrailEventHandlerRecorder{}
	g, _ := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithMaxStagnantGenerations(3),
		WithGuardrailEventHandler(rec.handle),
	)

	ctx := context.Background()

	// Build up stagnation state.
	for i := 0; i < 4; i++ {
		g.PostEvolveCheck(ctx, 75.0, i+1, nil)
	}

	// PreEvolveCheck can trigger both stagnation (warning) and unevaluated
	// population (critical) when given a high unevaluated ratio.
	result := g.PreEvolveCheck(ctx, 75.0, 5, 100, 60)

	// Should have triggered at least one event.
	assert.GreaterOrEqual(t, len(result.Events), 1)
	assert.Equal(t, len(result.Events), rec.count(),
		"handler call count must match result event count")

	// Verify handler received the same events as the result.
	allEvents := rec.all()
	for i, event := range result.Events {
		assert.Equal(t, event.Rule, allEvents[i].Rule)
		assert.Equal(t, event.ErrorCode, allEvents[i].ErrorCode)
	}
}

func TestGuardrailEventHandler_StagnationWarningReachesHandler(t *testing.T) {
	defer discardLogs()()
	rec := &guardrailEventHandlerRecorder{}
	g, _ := NewEvolutionGuardrails(
		WithMaxStagnantGenerations(3),
		WithGuardrailEventHandler(rec.handle),
	)

	ctx := context.Background()

	// Cause stagnation: call PostEvolveCheck without improvement.
	for i := 0; i < 4; i++ {
		g.PostEvolveCheck(ctx, 75.0, i+1, nil)
	}

	// PreEvolveCheck should trigger stagnation warning.
	g.PreEvolveCheck(ctx, 75.0, 5, 100, 10)

	require.Equal(t, 1, rec.count())
	event := rec.last()
	assert.Equal(t, GuardrailWarning, event.Level)
	assert.Equal(t, "stagnation", event.Rule)
	assert.Equal(t, ErrCodeStagnation, event.ErrorCode)
	assert.Contains(t, event.Message, "no improvement for")
}
