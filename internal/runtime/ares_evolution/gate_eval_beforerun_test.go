// gate_eval_beforerun_test.go locks REVIEW 3.4#9: beforeRun pushes
// candidate state into the SHARED executor behind the runner, but Check can
// run concurrently (the lifecycle runs gates outside its mutex). The push
// and the suite run it configures are now one serialized critical section;
// pre-fix, concurrent Checks interleaved their state pushes and each
// candidate's suite scored a mix of both candidates' settings.
//
// Run under -race: the shared executor state is deliberately unsynchronized
// so the race detector also flags the pre-fix unsynchronized access.
package evolution

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// sharedExecutorState is the stand-in for the shared executor state that
// beforeRun mutates (deliberately unsynchronized — exactly like the real
// executor behind the runner).
type sharedExecutorState struct {
	applied string
}

func (s *sharedExecutorState) Execute(_ context.Context, _ string) (string, []string, int, error) {
	return s.applied, nil, 1, nil
}

// markerEvaluator scores a run by the marker the executor reported, so a
// Check's returned score identifies WHICH candidate's state the suite
// actually ran with.
type markerEvaluator struct{}

func (markerEvaluator) Evaluate(_ context.Context, _ eval.TestCase, result eval.TestResult) ([]eval.EvalScore, error) {
	var score float64
	_, err := fmt.Sscanf(result.ActualOutput, "score:%f", &score)
	if err != nil {
		score = 0
	}
	return []eval.EvalScore{{Metric: "fidelity", Score: score}}, nil
}

func TestEvalGateBeforeRunConcurrentCandidates(t *testing.T) {
	shared := &sharedExecutorState{}

	runner, err := eval.NewAgentTestRunner(shared)
	require.NoError(t, err)
	registry := eval.NewEvaluatorRegistry()
	require.NoError(t, registry.Register("marker", markerEvaluator{}))
	runner.SetRegistry(registry)

	suite := eval.TestSuite{TestCases: []eval.TestCase{
		{ID: "case-1", Input: "hello"},
		{ID: "case-2", Input: "world"},
	}}

	cfg := DefaultEvalGateConfig()
	cfg.EvaluatorName = "marker"
	cfg.MinScore = 0 // every candidate passes; we assert the returned score

	gate := NewEvalGate(registry, runner, suite, cfg,
		WithEvalGateBeforeRun(func(cand *mutation.Strategy) {
			// Push candidate state into the shared executor.
			shared.applied = "score:" + cand.MutationDesc
		}),
	)

	const candidates = 6
	candScores := make([]float64, candidates)
	for i := range candScores {
		candScores[i] = float64(i+1) / 10
	}

	var wg sync.WaitGroup
	for i := 0; i < candidates; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cand := &mutation.Strategy{
				ID:                   fmt.Sprintf("cand-%d", idx),
				MutationDesc:         fmt.Sprintf("%.1f", candScores[idx]),
				StrategyMutationType: mutation.MutationType(0),
			}
			pass, score, reason := gate.Check(context.Background(), cand, nil)
			assert.True(t, pass, "candidate %d must pass: %s", idx, reason)
			// The suite must have run with THIS candidate's state.
			assert.InDelta(t, candScores[idx], score, 0.0001,
				"candidate %d scored with another candidate's executor state: got %.2f, want %.2f",
				idx, score, candScores[idx])
		}(i)
	}
	wg.Wait()
}
