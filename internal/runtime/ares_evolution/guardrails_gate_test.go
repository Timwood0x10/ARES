package evolution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// newGuardedAdapter builds a population adapter whose population is entirely
// unevaluated (every variant carries genome.ScoreUnevaluated, which is what
// mockGenomeMutator produces), with the given guardrails attached.
// generation seeds Population.Generation: 0 is the cold-start bootstrap
// population (guardrail-exempt), >=1 an established one (guarded).
func newGuardedAdapter(t *testing.T, g *EvolutionGuardrails, generation int) *GenomePopulationAdapter {
	t.Helper()
	base := &mutation.Strategy{
		ID:        "base",
		Params:    map[string]any{"temperature": 0.7},
		Score:     genome.ScoreUnevaluated,
		CreatedAt: time.Now(),
	}
	pop, err := genome.NewPopulation(context.Background(), base, &mockGenomeMutator{},
		genome.WithPopulationSize(10),
	)
	require.NoError(t, err)
	pop.Generation = generation

	crosser, err := genome.NewCrossover(genome.WithSeed(1))
	require.NoError(t, err)

	opts := []GenomeAdapterOption{}
	if g != nil {
		opts = append(opts, WithAdapterGuardrails(g))
	}
	adapter, err := NewGenomePopulationAdapter(pop, &mockGenomeMutator{}, crosser, opts...)
	require.NoError(t, err)
	return adapter
}

// TestAdapterPreGuardrailsBlockUnevaluatedPopulation is the behavioural
// contract for the adapter layer, in both postures:
//
//   - generation 0 (cold start): the bootstrap population is unevaluated BY
//     DEFINITION — the first cycle is what scores it. Pre-fix the guardrail
//     blocked that cycle forever (GA soak: unevaluated 19/20 every tick,
//     generation stuck at 0). The adapter must NOT gate here.
//   - generation >= 1 with an unevaluated majority: the guardrail's Critical
//     pre-condition fires and the cycle blocks.
func TestAdapterPreGuardrailsBlockUnevaluatedPopulation(t *testing.T) {
	ctx := context.Background()
	g, err := NewEvolutionGuardrails()
	require.NoError(t, err)

	t.Run("cold start is exempt", func(t *testing.T) {
		adapter := newGuardedAdapter(t, g, 0)
		require.Positive(t, adapter.PopulationUnevaluated(),
			"fixture must be majority-unevaluated")
		assert.NoError(t, adapter.runPreGuardrails(ctx),
			"generation 0 cold start must not be blocked — the first cycle evaluates the population")
	})

	t.Run("established generation blocks", func(t *testing.T) {
		adapter := newGuardedAdapter(t, g, 3)
		err := adapter.runPreGuardrails(ctx)
		require.Error(t, err, "a majority-unevaluated population at generation>=1 must block the cycle")
		assert.Contains(t, err.Error(), "pre-evolve guardrail check failed")
	})
}

// TestAdapterPreGuardrailsNilPassesThrough pins the nil-guardrails behavior as
// the
// explicit no-guardrails contract rather than an accident: paths that wire no
// guardrails (tests, minimal configs) must still run, so the nil check is a
// deliberate opt-out, not the default.
func TestAdapterPreGuardrailsNilPassesThrough(t *testing.T) {
	adapter := newGuardedAdapter(t, nil, 0)
	assert.NoError(t, adapter.runPreGuardrails(context.Background()),
		"without guardrails the adapter must not gate (documented opt-out)")
}

// TestSchedulerTickBlockedByGuardrails is the behavioural contract for the
// legacy scheduler path at an ESTABLISHED generation: a population the
// guardrail objects to must prevent adapter.Run from being called at all,
// not merely log a warning. The cold-start (generation 0) posture — where
// the guardrail must NOT block — is pinned by
// TestAdapterPreGuardrailsBlockUnevaluatedPopulation/cold-start and
// TestPreEvolveCheck_ColdStartUnevaluatedExempt.
func TestSchedulerTickBlockedByGuardrails(t *testing.T) {
	ctx := context.Background()
	// A real adapter over a fully-unevaluated population at an established
	// generation: it implements populationInspector, so the scheduler sees
	// the population shape and the cold-start exemption does not apply.
	adapter := newGuardedAdapter(t, nil, 4)

	g, err := NewEvolutionGuardrails()
	require.NoError(t, err)

	s := NewEvolutionScheduler(nil, adapter,
		WithMinInterval(time.Nanosecond),
		WithSchedulerGuardrails(g),
	)
	s.SetEnabled(true)

	// Seed a degradation signal so shouldEvolve says yes — otherwise the test
	// would pass for the wrong reason (throttling, not the guardrail).
	for i := 0; i < 30; i++ {
		s.RecordScore(taskScoreSuccess)
	}
	for i := 0; i < 10; i++ {
		s.RecordScore(taskScoreFailure)
	}
	require.True(t, s.shouldEvolve(ctx, CallbackData{AgentID: "test"}),
		"the window must be evolve-eligible, otherwise this test proves nothing")

	require.Positive(t, adapter.PopulationUnevaluated(),
		"the fixture must have unevaluated individuals for the guardrail to object to")
	assert.False(t, s.checkGuardrails(ctx),
		"an unevaluated-majority population at generation>=1 must block the cycle")

	// The verdict must actually gate execution, not merely be logged.
	genBefore := adapter.PopulationGeneration()
	s.Tick(ctx)
	assert.Equal(t, genBefore, adapter.PopulationGeneration(),
		"a blocking guardrail must prevent the generation from advancing")
}

// TestSchedulerCheckGuardrailsNilPassesThrough pins the opt-out contract for
// the scheduler side, mirroring the adapter test above.
func TestSchedulerCheckGuardrailsNilPassesThrough(t *testing.T) {
	s := NewEvolutionScheduler(nil, newMockAdapterForScheduler(), WithEnabled(true))
	assert.True(t, s.checkGuardrails(context.Background()),
		"without guardrails the scheduler must not gate (documented opt-out)")
}

// TestSchedulerGuardrailsSeeRealPopulationShape locks fix (2) from the
// TickBlocked test on its own: an adapter that reports its population must
// have that shape forwarded to PreEvolveCheck. Without this the guardrail is
// configured but blind, which is indistinguishable from not being wired at
// all. Driven at an established generation so the guardrail actually fires
// an event carrying the shape.
func TestSchedulerGuardrailsSeeRealPopulationShape(t *testing.T) {
	ctx := context.Background()
	adapter := newGuardedAdapter(t, nil, 2)

	var gotGeneration int
	g, err := NewEvolutionGuardrails(
		WithGuardrailEventHandler(func(evt GuardrailEvent) {
			gotGeneration = evt.Generation
		}),
	)
	require.NoError(t, err)

	s := NewEvolutionScheduler(nil, adapter, WithSchedulerGuardrails(g))
	s.SetEnabled(true)
	require.False(t, s.checkGuardrails(ctx))

	gotPop := adapter.PopulationSize()
	gotUnevaluated := adapter.PopulationUnevaluated()
	assert.Equal(t, 10, gotPop, "population size must reach the guardrail")
	assert.Positive(t, gotUnevaluated, "unevaluated count must reach the guardrail")
	assert.Equal(t, adapter.PopulationGeneration(), gotGeneration,
		"guardrail events must carry the real generation, not a hardcoded 0")
	assert.Equal(t, 2, gotGeneration, "the established-generation fixture must be visible in the event")
}
