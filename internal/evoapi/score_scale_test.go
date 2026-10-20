package evoapi

// score_scale_test.go is the regression guard for the seam this API had: it publishes
// GA scores (0-100) and fitness window means ([0,1]) on the same surface, both as a bare
// "score". Two different quantities that look identical in JSON are the failure mode
// behind the selection-blend bug (a [0,1] mean added to a 0-100 judge), so the scale is
// now published WITH the number and pinned here.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evolve "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestStrategyDTO_PublishesTheScoreScale pins the contract on the pure conversion:
// the score passes through untouched (it is the GA's own number) and the scale rides
// with it, so a consumer never has to know which producer it is looking at.
func TestStrategyDTO_PublishesTheScoreScale(t *testing.T) {
	t.Parallel()

	dto := publicStrategy(evolve.Strategy{ID: "s-1", Version: 3, Score: 87})
	assert.InDelta(t, 87, dto.Score, 1e-9, "the score is the GA's own value, not rescaled")
	assert.InDelta(t, evolve.JudgeScaleCeiling, dto.ScoreMax, 1e-9)

	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	// The wire name is "ScoreMax", not "score_max": THIS DTO carries no JSON tags, so
	// Go field names are the public contract (its siblings, e.g. GenerationStats, do use
	// snake_case). Renaming to match them would break every existing consumer, so the
	// inconsistency is recorded here instead of being fixed silently — see the ledger.
	assert.Contains(t, string(raw), `"ScoreMax"`,
		"the scale must be visible on the wire, not only in a Go comment")
	assert.NotContains(t, string(raw), `"score_max"`,
		"pin the actual wire name: if this DTO ever gains JSON tags, that is a breaking "+
			"API change and this assertion must be updated deliberately")
}

// TestJudgeScaleCeiling_IsTheDocumentedContract pins the number itself. It is
// deliberately a hard 100: every scorer wired in production (DeterministicScore base 50
// clamped [5,100], the LLM tier prompted for "0 to 100", ConstantScorer(50)) is on that
// scale, and a move to another one must be a deliberate edit here — not a silent change
// that leaves the selection blend, the tie-break band and this API annotating the wrong
// range.
func TestJudgeScaleCeiling_IsTheDocumentedContract(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, 100, evolve.JudgeScaleCeiling, 1e-9)
}

// TestGenerationStats_PublishesTheScoreScale covers the fitness-curve view: the same
// three scores on the same scale, annotated the same way, and provably NOT a [0,1] mean
// (the fixture's strategies score 50, which a normalised mean could never be).
func TestGenerationStats_PublishesTheScoreScale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := &mutation.Strategy{
		ID:        "scale-1",
		Params:    map[string]any{"temperature": 0.7},
		Score:     50.0,
		CreatedAt: time.Now(),
	}
	mut := &viewMutator{}
	crosser, err := genome.NewCrossover(genome.WithSeed(42))
	require.NoError(t, err)
	pop, err := genome.NewPopulation(ctx, base, mut, genome.WithPopulationSize(5), genome.WithEliteCount(1))
	require.NoError(t, err)
	adapter, err := evolve.NewGenomePopulationAdapter(pop, mut, crosser)
	require.NoError(t, err)
	view, err := NewGenomeWiringView(adapter)
	require.NoError(t, err)

	stats := view.Stats()
	require.NotNil(t, stats)
	assert.InDelta(t, evolve.JudgeScaleCeiling, stats.ScoreMax, 1e-9)

	// Every published score must sit inside the published range, and the range must be
	// the GA one — a window mean would be <= 1.
	assert.LessOrEqual(t, stats.BestScore, stats.ScoreMax)
	assert.GreaterOrEqual(t, stats.WorstScore, 0.0)
	if stats.ScoreMax > 1 {
		assert.Greater(t, stats.BestScore, 1.0,
			"a strategy scored 50: this surface reports GA scores, not [0,1] means")
	}

	for _, h := range view.FitnessHistory() {
		assert.InDelta(t, evolve.JudgeScaleCeiling, h.ScoreMax, 1e-9,
			"every history point carries the scale, not just the first")
	}
}
