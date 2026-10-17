package evolution

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ares_arena "github.com/Timwood0x10/ares/internal/runtime/arena"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// The arena regression gate contract: the gate is the RELATIVE complement to
// the absolute-score eval gate — it rejects ONLY a statistically significant
// drop against the active strategy (Confident && NewAvg < OldAvg), passes
// improvements and indistinguishable ties (the staging channel owns "is it
// better"), and fails CLOSED on scorer errors (an unverifiable candidate
// must not ride the promote path).

// scriptedScorer scores by the strategy's instructions: a fixed score per template.
//
// It reads the instructions the way the real scorers do — from either a bare string or a
// whole strategy value — because the gate now hands over the candidate itself so that its
// Params survive (see the gate's comment). Keeping the stub string-only is how a test
// ends up locking a lossy contract.
type scriptedScorer struct {
	scores map[string]float64
	// err, when set, fails every score call.
	err error
}

// scriptedInstructions mirrors the real scorers' extraction, including the typed shape.
func scriptedInstructions(strategy any) string {
	switch v := strategy.(type) {
	case string:
		return v
	case *mutation.Strategy:
		if v != nil {
			return v.PromptTemplate
		}
	case mutation.Strategy:
		return v.PromptTemplate
	}
	return ""
}

func (s scriptedScorer) Score(_ context.Context, input any) (float64, error) {
	if s.err != nil {
		return 0, s.err
	}
	tci, ok := input.(ares_arena.TestCaseInput)
	if !ok {
		return 0, errors.New("scriptedScorer: expected TestCaseInput")
	}
	strategy := scriptedInstructions(tci.Strategy)
	if v, ok := s.scores[strategy]; ok {
		return v, nil
	}
	return 0.5, nil
}

// capturingScorer records every strategy value the gate hands it.
type capturingScorer struct{ seen []any }

func (c *capturingScorer) Score(_ context.Context, input any) (float64, error) {
	tci, ok := input.(ares_arena.TestCaseInput)
	if !ok {
		return 0, errors.New("capturingScorer: expected TestCaseInput")
	}
	c.seen = append(c.seen, tci.Strategy)
	return 0.5, nil
}

// TestArenaRegressionGate_PassesTheWholeStrategy is the P0-1 replay contract: the gate
// hands the candidate ITSELF to the scorer, not just its prompt template.
//
// A template alone carries no Params, and Params is where a candidate declares its tool
// whitelist (agents.ParamKeyTools). A gate that scores a candidate by RUNNING it must run
// the candidate as declared — otherwise the evaluation grades a strategy no deployment
// would execute, and nothing in the score says so. The LLM scorer reads the template out
// of either shape, so carrying the whole value costs nothing.
func TestArenaRegressionGate_PassesTheWholeStrategy(t *testing.T) {
	active := &mutation.Strategy{ID: "active", PromptTemplate: "old-instructions",
		Params: map[string]any{"tools": "calculator"}}
	cand := &mutation.Strategy{ID: "cand", PromptTemplate: "new-instructions",
		Params: map[string]any{"tools": "calculator,hash_tool"}}
	captured := &capturingScorer{}

	g, err := NewArenaRegressionGate(captured, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)
	_, _, _ = g.Check(context.Background(), cand, active)

	require.NotEmpty(t, captured.seen, "the gate must actually score something")
	seenIDs := map[string]bool{}
	for _, s := range captured.seen {
		st, ok := s.(*mutation.Strategy)
		require.Truef(t, ok, "the strategy must travel as itself (got %T) — a template alone loses Params", s)
		assert.NotEmptyf(t, st.Params["tools"], "candidate %q reached the scorer without its tool whitelist", st.ID)
		seenIDs[st.ID] = true
	}
	assert.True(t, seenIDs["active"] && seenIDs["cand"],
		"both sides of the comparison must arrive as strategies, got %v", seenIDs)
}

func gateStrategies(old, new float64) (*mutation.Strategy, *mutation.Strategy, *scriptedScorer) {
	active := &mutation.Strategy{ID: "active", PromptTemplate: "old-instructions"}
	cand := &mutation.Strategy{ID: "cand", PromptTemplate: "new-instructions"}
	scorer := &scriptedScorer{scores: map[string]float64{
		"old-instructions": old,
		"new-instructions": new,
	}}
	return cand, active, scorer
}

func gateCases() []any {
	// Enough paired cases that Welch's t-test has variance to work with:
	// the scripted scorer returns identical scores per strategy, which the
	// tester treats as zero variance — a deterministic mean difference is
	// then significant, an equal mean is an exact tie.
	return []any{"case-1", "case-2", "case-3"}
}

func TestArenaRegressionGateRejectsSignificantDrop(t *testing.T) {
	cand, active, scorer := gateStrategies(0.9, 0.3)
	g, err := NewArenaRegressionGate(scorer, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)

	pass, score, reason := g.Check(context.Background(), cand, active)
	assert.False(t, pass, "a significant drop must be rejected")
	assert.Contains(t, reason, "regression")
	assert.Contains(t, reason, "0.900 -> 0.300")
	assert.Equal(t, int64(1), g.Rejected(), "rejection counter must advance")
	_ = score // informational win rate
}

func TestArenaRegressionGatePassesImprovement(t *testing.T) {
	cand, active, scorer := gateStrategies(0.3, 0.9)
	g, err := NewArenaRegressionGate(scorer, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)

	pass, _, reason := g.Check(context.Background(), cand, active)
	assert.True(t, pass, "a significant improvement must pass")
	assert.Contains(t, reason, "significant improvement")
	assert.Equal(t, int64(0), g.Rejected())
}

func TestArenaRegressionGatePassesTie(t *testing.T) {
	cand, active, scorer := gateStrategies(0.6, 0.6)
	g, err := NewArenaRegressionGate(scorer, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)

	pass, _, reason := g.Check(context.Background(), cand, active)
	assert.True(t, pass, "an indistinguishable tie must pass (staging owns the better/worse verdict)")
	assert.Contains(t, reason, "no significant difference")
}

func TestArenaRegressionGateFailsClosedOnScorerError(t *testing.T) {
	cand, active, _ := gateStrategies(0.6, 0.6)
	g, err := NewArenaRegressionGate(&scriptedScorer{err: errors.New("llm down")}, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)

	pass, _, reason := g.Check(context.Background(), cand, active)
	assert.False(t, pass, "a scorer failure must reject (fail closed)")
	assert.Contains(t, reason, "fail-closed")
}

func TestArenaRegressionGateRejectsNilStrategies(t *testing.T) {
	cand, active, scorer := gateStrategies(0.6, 0.6)
	g, err := NewArenaRegressionGate(scorer, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)

	pass, _, reason := g.Check(context.Background(), nil, active)
	assert.False(t, pass)
	assert.Contains(t, reason, "nil candidate")

	pass, _, reason = g.Check(context.Background(), cand, nil)
	assert.False(t, pass)
	assert.Contains(t, reason, "nil candidate")
}

func TestArenaRegressionGateConstruction(t *testing.T) {
	_, active, scorer := gateStrategies(0.6, 0.6)

	_, err := NewArenaRegressionGate(nil, gateCases(), ArenaRegressionGateConfig{})
	assert.Error(t, err, "nil scorer is a construction error")

	_, err = NewArenaRegressionGate(scorer, nil, ArenaRegressionGateConfig{})
	assert.Error(t, err, "empty suite is a construction error (a gate that cannot discriminate must not be built)")

	// Defaults fill in for zero config values.
	g, err := NewArenaRegressionGate(scorer, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)
	assert.Equal(t, 5, g.cfg.Runs)
	assert.Equal(t, 0.55, g.cfg.MinWinRate)
	assert.Equal(t, 0.05, g.cfg.Confidence)
	assert.Equal(t, DefaultRegressionGateTimeout, g.cfg.Timeout)

	assert.Equal(t, "arena_regression", g.Name())
	_ = active
}

func TestArenaRegressionGateConcurrentCheck(t *testing.T) {
	// The lifecycle runs gates OUTSIDE its mutex (Submit unlocks before the
	// gate loop), so concurrent Submits run Check concurrently. The
	// observability counters must be atomic — this test would fail under
	// -race with plain increments.
	cand, active, scorer := gateStrategies(0.9, 0.3)
	g, err := NewArenaRegressionGate(scorer, gateCases(), ArenaRegressionGateConfig{})
	require.NoError(t, err)

	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Check(context.Background(), cand, active)
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(n), g.Checked(), "every Check must be counted")
	assert.Equal(t, int64(n), g.Rejected(), "every significant drop must be counted")
}
