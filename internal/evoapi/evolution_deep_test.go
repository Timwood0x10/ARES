package evoapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evolve "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
)

// ── DefaultDreamCycleConfig ─────────────────────────────────────────────────

func TestDefaultDreamCycleConfig(t *testing.T) {
	cfg := DefaultDreamCycleConfig()
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 10, cfg.MinTasksBeforeEvolve)
	assert.InDelta(t, 0.15, cfg.MinScoreDrop, 1e-9)
	assert.Equal(t, 3, cfg.MaxMutations)
	assert.InDelta(t, 0.55, cfg.MinWinRate, 1e-9)
	assert.Equal(t, 50, cfg.TaskSampleSize)
	assert.Equal(t, 5, cfg.QuickRejectRuns)
	assert.Positive(t, cfg.Cooldown)
}

// ── parseCrossoverType ──────────────────────────────────────────────────────

func TestParseCrossoverType(t *testing.T) {
	tests := []struct {
		name    string
		give    string
		wantErr bool
	}{
		{"empty_defaults_uniform", "", false},
		{"uniform", "uniform", false},
		{"scattered", "scattered", false},
		{"two_point", "two_point", false},
		{"segment", "segment", false},
		{"single_point_rejected", "single_point", true},
		{"unknown_rejected", "bogus", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCrossoverType(tt.give)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			_ = got
		})
	}
}

func TestParseCrossoverType_UniformReturnsZero(t *testing.T) {
	got, err := parseCrossoverType("uniform")
	require.NoError(t, err)
	assert.Equal(t, genome.CrossoverUniform, got)
}

// ── defaultParamRanges ──────────────────────────────────────────────────────

func TestDefaultParamRanges(t *testing.T) {
	ranges := defaultParamRanges()
	assert.Contains(t, ranges, "temperature")
	assert.Contains(t, ranges, "top_k")
	assert.Contains(t, ranges, "max_tokens")
	assert.NotEmpty(t, ranges["temperature"].Values)
	assert.NotEmpty(t, ranges["top_k"].Values)
	assert.NotEmpty(t, ranges["max_tokens"].Values)
}

// ── publicStrategy ──────────────────────────────────────────────────────────

func TestPublicStrategy(t *testing.T) {
	s := evolve.Strategy{
		ID:             "s1",
		Version:        2,
		Score:          0.75,
		ParentID:       "p1",
		PromptTemplate: "do X",
		Params:         map[string]any{"k": "v"},
	}
	got := publicStrategy(s)
	assert.Equal(t, "s1", got.ID)
	assert.Equal(t, 2, got.Version)
	assert.InDelta(t, 0.75, got.Score, 1e-9)
	assert.Equal(t, "p1", got.ParentID)
	assert.Equal(t, "do X", got.PromptTemplate)
	assert.Equal(t, map[string]any{"k": "v"}, got.Params)
}

// ── NewDreamCycle error paths ───────────────────────────────────────────────

func TestNewDreamCycle_WrongSchedulerType(t *testing.T) {
	_, err := NewDreamCycle("not-a-scheduler", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scheduler must be")
}

func TestNewDreamCycle_WrongMutatorType(t *testing.T) {
	// scheduler is correct type but mutator is not
	sched := &evolve.EvolutionScheduler{}
	_, err := NewDreamCycle(sched, "not-a-mutator")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mutator must be")
}

func TestNewDreamCycle_WrongOptType(t *testing.T) {
	sched := &evolve.EvolutionScheduler{}
	mut := &stubEvolveMutator{}
	_, err := NewDreamCycle(sched, mut, "not-an-option")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "opts[0] must be")
}

// stubEvolveMutator satisfies evolve.MutatorInterface.
type stubEvolveMutator struct{}

func (m *stubEvolveMutator) Mutate(_ context.Context, _ evolve.Strategy, _ int) ([]evolve.Strategy, error) {
	return nil, nil
}

// ── NewMutator with nil parent ──────────────────────────────────────────────

func TestMutatorAdapter_MutateNilParent(t *testing.T) {
	mut, err := NewMutator("", MutationConfig{})
	require.NoError(t, err)
	_, err = mut.Mutate(context.Background(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

// ── NewPopulation + populationAdapter methods ───────────────────────────────

func TestNewPopulation_Success(t *testing.T) {
	base := &Strategy{
		ID:     "base",
		Score:  0.5,
		Params: map[string]any{"temperature": 0.7},
	}
	cfg := DefaultPopulationConfig()
	pop, err := NewPopulation(base, cfg)
	require.NoError(t, err)
	require.NotNil(t, pop)

	assert.Positive(t, pop.Size())
	assert.Equal(t, 0, pop.CurrentGeneration())
	agents := pop.Agents()
	assert.NotEmpty(t, agents)
}

func TestPopulationAdapter_BestStrategy(t *testing.T) {
	base := &Strategy{
		ID:     "base",
		Score:  0.5,
		Params: map[string]any{"temperature": 0.7},
	}
	cfg := DefaultPopulationConfig()
	pop, err := NewPopulation(base, cfg)
	require.NoError(t, err)

	best := pop.BestStrategy()
	// BestStrategy may be nil if no agent has been scored yet
	if best != nil {
		assert.NotEmpty(t, best.ID)
	}
}

func TestPopulationAdapter_BestScore(t *testing.T) {
	base := &Strategy{ID: "base", Score: 0.5, Params: map[string]any{"temperature": 0.7}}
	pop, err := NewPopulation(base, DefaultPopulationConfig())
	require.NoError(t, err)
	_ = pop.BestScore()
}

func TestPopulationAdapter_ScoreAgents_NilScorer(t *testing.T) {
	base := &Strategy{ID: "base", Score: 0.5, Params: map[string]any{"temperature": 0.7}}
	pop, err := NewPopulation(base, DefaultPopulationConfig())
	require.NoError(t, err)
	// nil scorer should be a no-op, not panic
	pop.ScoreAgents(nil)
}

func TestPopulationAdapter_ScoreAgents_WithScorer(t *testing.T) {
	base := &Strategy{ID: "base", Score: 0.5, Params: map[string]any{"temperature": 0.7}}
	pop, err := NewPopulation(base, DefaultPopulationConfig())
	require.NoError(t, err)

	called := false
	pop.ScoreAgents(func(_ *Strategy) float64 {
		called = true
		return 0.9
	})
	assert.True(t, called)
}

func TestPopulationAdapter_Evolve_InvalidCrossover(t *testing.T) {
	base := &Strategy{ID: "base", Score: 0.5, Params: map[string]any{"temperature": 0.7}}
	cfg := DefaultPopulationConfig()
	cfg.CrossoverType = "single_point" // rejected by internal engine
	pop, err := NewPopulation(base, cfg)
	require.NoError(t, err)

	err = pop.Evolve(context.Background())
	assert.Error(t, err)
}

// ── testerAdapter.Run ───────────────────────────────────────────────────────

type mockTester struct {
	result *RegressionResult
	err    error
}

func (m *mockTester) Run(_ context.Context, _ RegressionConfig) (*RegressionResult, error) {
	return m.result, m.err
}

func TestWithTester_ReturnsOption(t *testing.T) {
	opt := WithTester(&mockTester{})
	assert.NotNil(t, opt)
}

// ── MutationConfig / PromotionCriteria ──────────────────────────────────────

func TestPromotionCriteriaFields(t *testing.T) {
	c := DefaultPromotionCriteria()
	assert.Equal(t, 100, c.MinSampleCount)
	assert.InDelta(t, 0.85, c.MinSuccessRate, 1e-9)
	assert.InDelta(t, 0.70, c.MinConfidence, 1e-9)
	assert.Equal(t, 5, c.ChampionHoldPeriod)
	assert.InDelta(t, 0.30, c.DemotionThreshold, 1e-9)
	assert.Equal(t, 20, c.MaxChampionTenure)
}

func TestNewPromoter_NilCriteria(t *testing.T) {
	p := NewPromoter(nil)
	assert.NotNil(t, p)
}

// ── publicStrategy roundtrip ────────────────────────────────────────────────

func TestPublicStrategy_PreservesParams(t *testing.T) {
	s := evolve.Strategy{
		ID:     "rt",
		Params: map[string]any{"temperature": 0.7, "top_k": 40},
	}
	got := publicStrategy(s)
	assert.Equal(t, "rt", got.ID)
	assert.Equal(t, 0.7, got.Params["temperature"])
	assert.Equal(t, 40, got.Params["top_k"])
}
