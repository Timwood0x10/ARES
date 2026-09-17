package evolution

// fitness_aggregator_pushdown_test.go locks REVIEW 2.4#26: the strategy_id
// (and tool_step_id) equality filters must be pushed into the store query so
// they compose with the LIMIT — the rollback judge gate counts the target
// strategy's OWN samples inside a WindowSize window. Filtering client-side
// AFTER a LIMIT-ed query meant that under multi-strategy traffic the window
// could consist entirely of OTHER strategies' records, the scoped count
// stayed 0, and the rollback safety net never opened.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// TestAggregator_Window_StrategyFilterPushedPastLimit seeds more recent
// records for OTHER strategies than the whole window size, then verifies
// the target strategy's own samples are still counted: the filter must run
// before the limit, not after.
func TestAggregator_Window_StrategyFilterPushedPastLimit(t *testing.T) {
	store := evidence.NewMemoryStore()
	cfg := DefaultAggregatorConfig() // WindowSize 50, MinSamplesBeforeJudge 10
	agg := NewRuntimeFitnessAggregator(store, cfg)
	ctx := context.Background()

	// 60 records for "noise-*" strategies, written LAST (newest timestamps)
	// — with a client-side filter these fill the entire 50-record window.
	for s := 0; s < 6; s++ {
		seedStrategyFitness(t, store, "noise-strategy", 0.9, 10)
	}
	// 20 own-strategy records, older than every noise record.
	seedStrategyFitness(t, store, "target-strategy", 0.4, 20)

	res := agg.Window(ctx, "target-strategy")

	// The judge gate must open: the target strategy's own 20 samples are
	// visible in its scoped window despite 60 newer foreign records.
	assert.True(t, res.Ok,
		"strategy-scoped window must count the target's own samples past the foreign noise (rollback safety net)")
	strategyStat, ok := res.PerSource["strategy"]
	require.True(t, ok, "strategy source must be present")
	assert.Equal(t, 20, strategyStat.Count, "all 20 own-strategy samples must be in the window")
	assert.InDelta(t, 0.4, strategyStat.Mean, 1e-9)
}

// TestAggregator_WindowToolStep_FilterPushedPastLimit does the same for the
// tool_step_id sub-filter on the tool_call channel.
func TestAggregator_WindowToolStep_FilterPushedPastLimit(t *testing.T) {
	store := evidence.NewMemoryStore()
	cfg := DefaultAggregatorConfig()
	agg := NewRuntimeFitnessAggregator(store, cfg)
	ctx := context.Background()

	seed := func(strategyID, toolStepID string, value float64, n int, base time.Time) {
		for i := 0; i < n; i++ {
			payload, err := json.Marshal(map[string]any{
				"value": value, "strategy_id": strategyID, "tool_step_id": toolStepID,
			})
			require.NoError(t, err)
			require.NoError(t, store.Append(ctx, evidence.Evidence{
				ID:        "tc_" + strategyID + "_" + toolStepID + "_" + string(rune('a'+i)),
				Source:    toolCallEvidenceSource,
				Kind:      evidence.KindFitness,
				Payload:   payload,
				Timestamp: base.Add(time.Duration(i) * time.Second),
			}))
		}
	}

	old := time.Now().Add(-2 * time.Hour)
	new := time.Now().Add(-time.Hour)
	// 60 newer records for a different tool step; 15 older records for the
	// target step — enough to satisfy MinSamplesBeforeJudge (10).
	seed("strategy-x", "other-step", 0.8, 60, new)
	seed("strategy-x", "target-step", 0.3, 15, old)

	res := agg.WindowToolStep(ctx, "strategy-x", "target-step")
	assert.True(t, res.Ok, "tool-step-scoped samples must be counted past foreign noise")
	assert.Equal(t, 15, res.Count)
	assert.InDelta(t, 0.3, res.Mean, 1e-9)
}
